package isa

// Machine is the architectural reference model of the nandbox computer. It
// is cycle-faithful where software can observe it: every instruction takes
// two clock cycles (fetch, execute) and the LFSR steps once per cycle, so a
// program reads the same random bytes here as on the gate-level CPU.
type Machine struct {
	R      [4]uint8
	PC     uint8
	Z      bool
	N      bool
	C      bool
	Halted bool
	ROM    [256]uint16
	RAM    [256]uint8
	VRAM   [256]uint8
	Score  uint8
	LEDs   uint8
	Joy    uint8
	LFSR   uint16
	Cycles uint64
}

// NewMachine loads a program image and initial RAM contents.
func NewMachine(code []uint16, data []uint8) *Machine {
	m := &Machine{}
	copy(m.ROM[:], code)
	copy(m.RAM[:], data)
	return m
}

func (m *Machine) tick() {
	m.LFSR = LFSRNext(m.LFSR)
	m.Cycles++
}

// readData returns what a load from the data space sees. Loads complete in
// the following fetch cycle, so RAND shows the LFSR two edges later.
func (m *Machine) readData(addr uint8) uint8 {
	if addr < IOBase {
		return m.RAM[addr]
	}
	switch addr & 3 {
	case 0:
		return m.Joy & 0x1F
	case 1:
		return uint8(m.LFSR)
	case 2:
		return m.Score
	default:
		return m.LEDs
	}
}

func (m *Machine) writeData(addr, v uint8) {
	if addr < IOBase {
		m.RAM[addr] = v
		return
	}
	switch addr & 3 {
	case 2:
		m.Score = v
	case 3:
		m.LEDs = v
	}
}

// Step executes one instruction (two clock cycles). A halted machine keeps
// ticking but changes no architectural state.
func (m *Machine) Step() {
	if m.Halted {
		m.tick()
		m.tick()
		return
	}
	in := Decode(m.ROM[m.PC])
	m.PC++
	m.tick()
	m.tick()
	b := in.Value
	if !in.Imm {
		b = m.R[in.Rs]
	}
	switch in.Op {
	case OpLD:
		if in.Space {
			m.R[in.Rd] = m.VRAM[b]
		} else {
			m.R[in.Rd] = m.readData(b)
		}
	case OpST:
		if in.Space {
			m.VRAM[b] = m.R[in.Rd]
		} else {
			m.writeData(b, m.R[in.Rd])
		}
	case OpJ:
		flag := true
		switch in.Cond & 3 {
		case CondZ:
			flag = m.Z
		case CondC:
			flag = m.C
		case CondN:
			flag = m.N
		}
		if flag != (in.Cond&CondInvert != 0) {
			m.PC = b
		}
	case OpJAL:
		m.R[in.Rd] = m.PC
		m.PC = b
	case OpHLT:
		m.Halted = true
	default:
		y, z, n, c := ALU(in.Op, m.R[in.Rd], b, m.C)
		if WritesRd(in.Op) {
			m.R[in.Rd] = y
		}
		if SetsFlags(in.Op) {
			m.Z, m.N, m.C = z, n, c
		}
	}
}
