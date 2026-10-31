package isa

import "testing"

func TestDecodeEncodeRoundTrip(t *testing.T) {
	for w := range 1 << 16 {
		in := Decode(uint16(w))
		var back uint16
		if in.Op == OpJ {
			back = EncodeJump(in.Cond, in.Imm, in.Value)
		} else {
			back = Encode(in.Op, in.Imm, in.Space, in.Rd, in.Value)
		}
		if back != uint16(w) {
			t.Fatalf("%#04x -> %+v -> %#04x", w, in, back)
		}
	}
}

func TestALUFlagSemantics(t *testing.T) {
	cases := []struct {
		op, a, b uint8
		cin      bool
		y        uint8
		z, n, c  bool
	}{
		{OpADD, 200, 100, false, 44, false, false, true},
		{OpADC, 1, 1, true, 3, false, false, false},
		{OpSUB, 5, 7, false, 0xFE, false, true, false}, // borrow: C clear
		{OpSUB, 7, 5, false, 2, false, false, true},
		{OpSBC, 10, 3, false, 6, false, false, true}, // C clear subtracts one more
		{OpCMP, 3, 3, false, 0, true, false, true},
		{OpAND, 0xF0, 0x0F, true, 0, true, false, false},
		{OpMOV, 0, 0x80, false, 0x80, false, true, false},
		{OpSHR, 0x81, 0, false, 0x40, false, false, true},
	}
	for _, c := range cases {
		y, z, n, cy := ALU(c.op, c.a, c.b, c.cin)
		if y != c.y || z != c.z || n != c.n || cy != c.c {
			t.Errorf("ALU(%s, %#x, %#x, %v) = %#x z%v n%v c%v", Mnemonics[c.op], c.a, c.b, c.cin, y, z, n, cy)
		}
	}
	if SetsFlags(OpMOV) || !SetsFlags(OpCMP) || SetsFlags(OpLD) || WritesRd(OpCMP) {
		t.Error("flag/write-back classification is wrong")
	}
}

func TestLFSRHasFullPeriod(t *testing.T) {
	seen := map[uint16]bool{}
	s := uint16(0)
	for range 1 << 16 {
		if seen[s] {
			break
		}
		seen[s] = true
		s = LFSRNext(s)
	}
	if len(seen) != 1<<16-1 {
		t.Fatalf("LFSR period %d, want 65535", len(seen))
	}
}

func TestEmulatorHaltsAndKeepsCycling(t *testing.T) {
	m := NewMachine([]uint16{Encode(OpMOV, true, false, 0, 7), Encode(OpHLT, false, false, 0, 0)}, nil)
	for range 5 {
		m.Step()
	}
	if !m.Halted || m.R[0] != 7 || m.PC != 2 || m.Cycles != 10 {
		t.Fatalf("halted=%v r0=%d pc=%d cycles=%d", m.Halted, m.R[0], m.PC, m.Cycles)
	}
}
