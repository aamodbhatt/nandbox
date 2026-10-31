// Package isa defines NB8, the 8-bit instruction set of the nandbox CPU:
// encoding, disassembly and a cycle-faithful reference emulator that the
// gate-level CPU is tested against. docs/ISA.md is the prose specification.
package isa

import "fmt"

// Opcodes (instruction bits 15..12).
const (
	OpADD uint8 = 0x0
	OpADC uint8 = 0x1
	OpSUB uint8 = 0x2
	OpSBC uint8 = 0x3
	OpAND uint8 = 0x4
	OpOR  uint8 = 0x5
	OpXOR uint8 = 0x6
	OpMOV uint8 = 0x7
	OpSHR uint8 = 0x8
	OpLD  uint8 = 0x9
	OpCMP uint8 = 0xA
	OpST  uint8 = 0xB
	OpTST uint8 = 0xC
	OpJ   uint8 = 0xD
	OpJAL uint8 = 0xE
	OpHLT uint8 = 0xF
)

// Mnemonics indexed by opcode.
var Mnemonics = [16]string{"add", "adc", "sub", "sbc", "and", "or", "xor", "mov", "shr", "ld", "cmp", "st", "tst", "j", "jal", "hlt"}

// Memory map of the data space (see docs/ISA.md).
const (
	IOBase    = 0xF0 // 0xF0..0xFF are I/O ports, partially decoded by addr & 3
	PortInput = 0xF0 // read: joystick bits
	PortRand  = 0xF1 // read: LFSR random byte
	PortScore = 0xF2 // read/write: 7-segment score display (two hex digits)
	PortLEDs  = 0xF3 // read/write: eight debug LEDs
)

// Joystick bits as seen at PortInput.
const (
	JoyUp    = 1 << 0
	JoyDown  = 1 << 1
	JoyLeft  = 1 << 2
	JoyRight = 1 << 3
	JoyFire  = 1 << 4
)

// Jump conditions: bit 2 inverts, bits 1..0 select always/Z/C/N.
const (
	CondAlways uint8 = 0
	CondZ      uint8 = 1
	CondC      uint8 = 2
	CondN      uint8 = 3
	CondInvert uint8 = 4
)

// Instr is a decoded instruction word.
type Instr struct {
	Op    uint8
	Imm   bool  // operand B is the 8-bit immediate (else register Rs)
	Space bool  // ld/st: video space instead of data space
	Rd    uint8 // destination / store source register
	Rs    uint8 // operand register (when !Imm)
	Value uint8 // 8-bit immediate
	Cond  uint8 // jumps: bits 10..8
}

// Decode splits a 16-bit instruction word into fields.
func Decode(w uint16) Instr {
	return Instr{
		Op:    uint8(w >> 12),
		Imm:   w>>11&1 == 1,
		Space: w>>10&1 == 1,
		Rd:    uint8(w >> 8 & 3),
		Rs:    uint8(w & 3),
		Value: uint8(w),
		Cond:  uint8(w >> 8 & 7),
	}
}

// Encode builds an instruction word from its fields.
func Encode(op uint8, imm, space bool, rd uint8, operand uint8) uint16 {
	w := uint16(op)<<12 | uint16(rd&3)<<8 | uint16(operand)
	if imm {
		w |= 1 << 11
	}
	if space {
		w |= 1 << 10
	}
	return w
}

// EncodeJump builds a jump word; operand is a target address or register.
func EncodeJump(cond uint8, imm bool, operand uint8) uint16 {
	w := uint16(OpJ)<<12 | uint16(cond&7)<<8 | uint16(operand)
	if imm {
		w |= 1 << 11
	}
	return w
}

var condNames = [8]string{"jmp", "jz", "jc", "jn", "nop", "jnz", "jnc", "jnn"}

// Disassemble renders an instruction word in assembler syntax.
func Disassemble(w uint16) string {
	in := Decode(w)
	src := fmt.Sprintf("r%d", in.Rs)
	if in.Imm {
		src = fmt.Sprintf("0x%02x", in.Value)
	}
	switch in.Op {
	case OpSHR:
		return fmt.Sprintf("shr r%d", in.Rd)
	case OpLD:
		if in.Space {
			return fmt.Sprintf("vld r%d, [%s]", in.Rd, src)
		}
		return fmt.Sprintf("ld r%d, [%s]", in.Rd, src)
	case OpST:
		if in.Space {
			return fmt.Sprintf("vst [%s], r%d", src, in.Rd)
		}
		return fmt.Sprintf("st [%s], r%d", src, in.Rd)
	case OpJ:
		if in.Cond == CondInvert|CondAlways {
			return "nop"
		}
		return fmt.Sprintf("%s %s", condNames[in.Cond], src)
	case OpJAL:
		return fmt.Sprintf("jal r%d, %s", in.Rd, src)
	case OpHLT:
		return "hlt"
	}
	return fmt.Sprintf("%s r%d, %s", Mnemonics[in.Op], in.Rd, src)
}

// ALU is the reference model of the ALU8 chip for all 16 function codes.
// Bit 2 selects the logic unit (AND/OR/XOR/pass-B by bits 1..0), code 8 is
// shift-right, everything else is the adder with B inverted by bit 1 and the
// carry flag used as carry-in when bit 0 is set (and bit 3 clear).
func ALU(op, a, b uint8, cin bool) (y uint8, z, n, c bool) {
	switch {
	case op&4 != 0:
		switch op & 3 {
		case 0:
			y = a & b
		case 1:
			y = a | b
		case 2:
			y = a ^ b
		default:
			y = b
		}
	case op == OpSHR:
		y, c = a>>1, a&1 == 1
	default:
		inv := op&2 != 0
		bb := b
		if inv {
			bb = ^b
		}
		ci := inv
		if op&1 != 0 && op&8 == 0 {
			ci = cin
		}
		sum := uint16(a) + uint16(bb)
		if ci {
			sum++
		}
		y, c = uint8(sum), sum > 0xFF
	}
	return y, y == 0, y&0x80 != 0, c
}

// SetsFlags reports whether an opcode updates Z, N and C (every ALU op but MOV).
func SetsFlags(op uint8) bool {
	return op <= OpSHR && op != OpMOV || op == OpCMP || op == OpTST
}

// WritesRd reports whether an ALU opcode writes its result to Rd.
func WritesRd(op uint8) bool { return op <= OpSHR }

// LFSRNext steps the 16-bit Fibonacci LFSR (taps 16,14,13,11) with XNOR
// feedback, so the all-zero power-on state is part of the sequence.
func LFSRNext(s uint16) uint16 {
	fb := ^(s>>15 ^ s>>13 ^ s>>12 ^ s>>10) & 1
	return s<<1 | fb
}
