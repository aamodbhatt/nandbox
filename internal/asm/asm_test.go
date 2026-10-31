package asm

import (
	"errors"
	"strings"
	"testing"

	"github.com/aamodbhatt/nandbox/internal/isa"
)

func mustAssemble(t *testing.T, src string, opts ...Option) *Program {
	t.Helper()
	p, err := Assemble(src, opts...)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return p
}

// Every instruction form round-trips through the disassembler.
func TestEncodingRoundTripsThroughDisassembler(t *testing.T) {
	lines := []string{
		"add r0, r1", "adc r1, 0x05", "sub r2, 0xff", "sbc r3, r0", "and r0, 0x0f", "or r1, r2", "xor r2, 0x80",
		"mov r3, 0x2a", "shr r1", "ld r0, [0x10]", "ld r1, [r2]", "vld r2, [r3]", "st [0xf2], r0", "vst [r1], r2",
		"cmp r0, 0x0a", "tst r1, 0x01", "jmp 0x00", "jz 0x04", "jnz r3", "jc 0x01", "jnc 0x02", "jn 0x03", "jnn 0x05",
		"jal r3, 0x20", "hlt", "nop",
	}
	p := mustAssemble(t, strings.Join(lines, "\n"))
	for i, want := range lines {
		if got := isa.Disassemble(p.Code[i]); got != want {
			t.Errorf("line %d: %q disassembles as %q", i+1, want, got)
		}
	}
}

func TestLabelsConstantsAndData(t *testing.T) {
	p := mustAssemble(t, `
        .equ BASE, 0x20
        .equ MASK, (1 << 4) - 1
        .data
v:      .byte 1, 2, -1, 'A'
msg:    .ascii "hi;there"     ; the ';' inside quotes is not a comment
pad:    .zero 3
end:
        .equ LEN, end - v
        .code
start:  mov r0, BASE + MASK
        ld  r1, [msg]
        jmp start
        call sub
sub:    ret
`)
	if got := p.Symbols["MASK"]; got != 15 {
		t.Fatalf("MASK = %d", got)
	}
	if p.Symbols["msg"] != 4 || p.Symbols["LEN"] != 15 || p.Symbols["sub"] != 4 {
		t.Fatalf("symbols wrong: %v", p.Symbols)
	}
	if want := []uint8{1, 2, 0xff, 'A', 'h', 'i', ';'}; string(p.Data[:7]) != string(want) {
		t.Fatalf("data = %v", p.Data[:7])
	}
	if got := isa.Disassemble(p.Code[0]); got != "mov r0, 0x2f" {
		t.Fatalf("expression operand: %s", got)
	}
	if got := isa.Disassemble(p.Code[3]); got != "jal r3, 0x04" {
		t.Fatalf("call encodes as %s", got)
	}
}

func TestDefinesOverrideEqu(t *testing.T) {
	p := mustAssemble(t, ".equ DELAY, 200\nmov r0, DELAY", WithDefine("DELAY", 3))
	if got := isa.Disassemble(p.Code[0]); got != "mov r0, 0x03" {
		t.Fatalf("define ignored: %s", got)
	}
}

func TestErrorsCarryLineAndColumn(t *testing.T) {
	cases := []struct{ src, want string }{
		{"  frob r1", "1:3: unknown instruction \"frob\""},
		{"add r0, 1\n  jmp nowhere", "2:7: undefined symbol \"nowhere\""},
		{"mov r5, 1", "1:5: expected a register r0..r3"},
		{"mov r0, 300", "1:9: value 300 does not fit in 8 bits"},
		{"x: nop\nx: nop", "2:1: symbol \"x\" already defined"},
		{"ld r0, 0x10", "1:8: expected a memory operand"},
		{".byte 1", "1:1: .byte is only allowed in the .data section"},
		{"add r0", "1:1: add takes 2 operand(s), got 1"},
		{"mov r0, (1 + 2", "1:15: missing ')'"},
	}
	for _, c := range cases {
		_, err := Assemble(c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: got %v, want %q", c.src, err, c.want)
		}
		var ae *Error
		if err != nil && !errors.As(err, &ae) {
			t.Errorf("%q: error is not an *asm.Error", c.src)
		}
	}
}

func TestProgramSizeLimit(t *testing.T) {
	_, err := Assemble(strings.Repeat("nop\n", 257))
	if err == nil || !strings.Contains(err.Error(), "exceeds 256 instructions") {
		t.Fatalf("got %v", err)
	}
}
