package machine

import (
	"math/rand/v2"
	"testing"

	"github.com/aamodbhatt/nandbox/internal/asm"
	"github.com/aamodbhatt/nandbox/internal/hdl"
	"github.com/aamodbhatt/nandbox/internal/isa"
	"github.com/aamodbhatt/nandbox/internal/sim"
)

func build(t *testing.T, code []uint16, data []uint8) *Machine {
	t.Helper()
	m, err := New(hdl.Program{Code: code, Data: data}, sim.ModeEvent)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assemble(t *testing.T, src string, opts ...asm.Option) *asm.Program {
	t.Helper()
	p, err := asm.Assemble(src, opts...)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return p
}

// TestEveryOpcode runs one small program per opcode on the gate-level CPU and
// checks the destination register and all three flags.
func TestEveryOpcode(t *testing.T) {
	cases := []struct {
		name, src string
		reg       int
		want      uint8
		z, n, c   bool
	}{
		{"add", "mov r0, 200\nadd r0, 100", 0, 44, false, false, true},
		{"add-zero", "mov r0, 0x80\nadd r0, 0x80", 0, 0, true, false, true},
		{"adc", "mov r0, 0xFF\nadd r0, 1\nmov r1, 5\nadc r1, 5", 1, 11, false, false, false},
		{"sub", "mov r0, 5\nsub r0, 7", 0, 0xFE, false, true, false},
		{"sub-nb", "mov r0, 7\nsub r0, 5", 0, 2, false, false, true},
		{"sbc-borrow", `mov r0, 0
cmp r0, 1
mov r2, 9
mov r1, 10
sbc r1, 3`, 1, 6, false, false, true},
		{"and", "mov r0, 0xF0\nand r0, 0x3C", 0, 0x30, false, false, false},
		{"or", "mov r0, 0x80\nor r0, 1", 0, 0x81, false, true, false},
		{"xor", "mov r0, 0x55\nxor r0, 0x55", 0, 0, true, false, false},
		{"mov-keeps-flags", `mov r0, 0
add r0, 0
mov r2, 0x99
mov r1, r2`, 1, 0x99, true, false, false},
		{"shr", "mov r3, 0x81\nshr r3", 3, 0x40, false, false, true},
		{"cmp", "mov r0, 3\ncmp r0, 3", 0, 3, true, false, true},
		{"tst", "mov r0, 0xF0\ntst r0, 0x0F", 0, 0xF0, true, false, false},
		{"ld-st", ".data\nv: .byte 0x42\n.code\nld r1, [v]\nmov r0, 0\nst [0x10], r1\nld r2, [0x10]", 2, 0x42, true, false, false},
		{"vld-vst", "mov r0, 0x77\nmov r1, 0x20\nvst [r1], r0\nmov r0, 0\nvld r3, [0x20]", 3, 0x77, true, false, false},
		{"jmp", "mov r0, 1\njmp skip\nmov r0, 2\nskip: add r0, 0", 0, 1, false, false, false},
		{"jz-taken", "mov r0, 0\nadd r0, 0\njz ok\nmov r1, 9\nok: mov r1, 4", 1, 4, false, false, false},
		{"jnz-not-taken", "mov r0, 0\nadd r0, 0\njnz bad\nmov r1, 6\nhlt\nbad: mov r1, 9", 1, 6, false, false, false},
		{"jc", "mov r0, 0xFF\nadd r0, 1\njc ok\nmov r1, 1\nhlt\nok: mov r1, 2", 1, 2, false, false, false},
		{"jnc", "mov r0, 1\nadd r0, 1\njnc ok\nmov r1, 1\nhlt\nok: mov r1, 3", 1, 3, false, false, false},
		{"jn", `mov r0, 0x80
or r0, 0
jn ok
mov r1, 1
hlt
ok: mov r1, 5`, 1, 5, false, false, false},
		{"jnn", "mov r0, 1\njnn ok\nmov r1, 1\nhlt\nok: mov r1, 7", 1, 7, false, false, false},
		{"jal-ret", "call f\nmov r1, 8\nhlt\nf: mov r0, 1\nret", 3, 1, false, false, false},
		{"jmp-reg", "mov r2, 4\njmp r2\nmov r1, 1\nhlt\nmov r1, 2", 1, 2, false, false, false},
		{"io-score-leds", "mov r0, 0x12\nst [0xF2], r0\nld r1, [0xF2]\nmov r0, 0xA5\nst [0xF3], r0\nld r2, [0xF3]\nmov r0, r1", 0, 0x12, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := assemble(t, tc.src+"\nhlt")
			m := build(t, p.Code, p.Data)
			if err := m.RunUntilHalt(200); err != nil {
				t.Fatal(err)
			}
			if got := m.Reg(tc.reg); got != tc.want {
				t.Fatalf("r%d = %#x, want %#x", tc.reg, got, tc.want)
			}
			// Flags are checked for cases whose last flag-setting op is the one under test.
			if tc.name != "jal-ret" && tc.name != "ld-st" && tc.name != "vld-vst" && tc.name[0] != 'j' && tc.name != "io-score-leds" {
				if z, n, c := m.Flags(); z != tc.z || n != tc.n || c != tc.c {
					t.Fatalf("flags z,n,c = %v,%v,%v want %v,%v,%v", z, n, c, tc.z, tc.n, tc.c)
				}
			}
		})
	}
}

func compareState(t *testing.T, step int, m *Machine, ref *isa.Machine) {
	t.Helper()
	for i := range 4 {
		if m.Reg(i) != ref.R[i] {
			t.Fatalf("step %d: r%d gate=%#x ref=%#x", step, i, m.Reg(i), ref.R[i])
		}
	}
	if z, n, c := m.Flags(); z != ref.Z || n != ref.N || c != ref.C {
		t.Fatalf("step %d: flags gate=%v%v%v ref=%v%v%v", step, z, n, c, ref.Z, ref.N, ref.C)
	}
	wantPC := ref.PC + 1 // the gate machine has already fetched the next instruction
	if ref.Halted {
		wantPC = ref.PC
	}
	if m.PC() != wantPC {
		t.Fatalf("step %d: pc gate=%#x ref=%#x", step, m.PC(), ref.PC)
	}
	if m.Score() != ref.Score || m.LEDs() != ref.LEDs {
		t.Fatalf("step %d: io gate=%#x/%#x ref=%#x/%#x", step, m.Score(), m.LEDs(), ref.Score, ref.LEDs)
	}
	ram, vram := m.RAM(), m.VRAM()
	for a := range 256 {
		if a < isa.IOBase && uint8(ram[a]) != ref.RAM[a] {
			t.Fatalf("step %d: ram[%#x] gate=%#x ref=%#x", step, a, ram[a], ref.RAM[a])
		}
		if uint8(vram[a]) != ref.VRAM[a] {
			t.Fatalf("step %d: vram[%#x] gate=%#x ref=%#x", step, a, vram[a], ref.VRAM[a])
		}
	}
}

// TestLockstepWithReferenceEmulator runs random programs (every instruction
// word is valid) on the gate-level CPU and the Go emulator side by side.
func TestLockstepWithReferenceEmulator(t *testing.T) {
	for seed := range uint64(12) {
		r := rand.New(rand.NewPCG(seed, 99))
		code := make([]uint16, 256)
		for i := range code {
			code[i] = uint16(r.UintN(1 << 16))
			if code[i]>>12 == uint16(isa.OpHLT) && r.IntN(4) != 0 {
				code[i] &^= 0x1000 // keep most programs running
			}
		}
		data := make([]uint8, 64)
		for i := range data {
			data[i] = uint8(r.UintN(256))
		}
		m := build(t, code, data)
		ref := isa.NewMachine(code, data)
		joy := uint8(r.UintN(32))
		ref.Joy = joy
		if err := m.SetJoystick(joy); err != nil {
			t.Fatal(err)
		}
		// A load writes its register in the next fetch cycle, so the gate-level
		// machine is sampled one cycle late: after execute(k) + fetch(k+1).
		if err := m.Cycle(); err != nil {
			t.Fatal(err)
		}
		for step := range 400 {
			for range 2 {
				if err := m.Cycle(); err != nil {
					t.Fatal(err)
				}
			}
			ref.Step()
			compareState(t, step, m, ref)
		}
	}
}

func TestFibProgram(t *testing.T) {
	p := assemble(t, source(t, "fib"))
	m := build(t, p.Code, p.Data)
	if err := m.RunUntilHalt(2000); err != nil {
		t.Fatal(err)
	}
	want := []uint8{0, 1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233}
	for i, v := range want {
		if uint8(m.RAM()[i]) != v {
			t.Fatalf("fib[%d] = %d, want %d", i, m.RAM()[i], v)
		}
	}
	if m.LEDs() != uint8(len(want)) {
		t.Fatalf("LEDs = %d, want %d", m.LEDs(), len(want))
	}
}

func TestCounterProgram(t *testing.T) {
	p := assemble(t, source(t, "counter"), asm.WithDefine("DELAY", 1))
	m := build(t, p.Code, p.Data)
	for range 200 {
		if err := m.Step(); err != nil {
			t.Fatal(err)
		}
	}
	// 7 instructions per count with DELAY=1, after a 1-instruction preamble.
	if got := m.LEDs(); got < 26 || got > 30 || m.Score() != got {
		t.Fatalf("after 200 instructions LEDs=%d score=%d", got, m.Score())
	}
}

func TestHelloBanner(t *testing.T) {
	p := assemble(t, source(t, "hello"), asm.WithDefine("DELAY", 1))
	m := build(t, p.Code, p.Data)
	off, text := p.Symbols["off"], p.Symbols["text"]
	length := p.Symbols["LEN"]
	for range 4000 {
		if m.RAM()[off] == 1 {
			break
		}
		if err := m.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if m.RAM()[off] != 1 {
		t.Fatal("first frame never finished")
	}
	vram := m.VRAM()
	for c := range 16 {
		bitsCol := p.Data[text+c%length]
		for r := range 7 {
			want := uint16(0)
			if bitsCol>>uint(r)&1 == 1 {
				want = uint16(15 - r)
			}
			if got := vram[(4+r)*16+c]; got != want {
				t.Fatalf("pixel row %d col %d = %d, want %d", 4+r, c, got, want)
			}
		}
	}
}
