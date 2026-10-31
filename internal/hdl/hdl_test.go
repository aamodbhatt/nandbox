package hdl

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/isa"
	"github.com/aamodbhatt/nandbox/internal/sim"
)

var (
	stdOnce sync.Once
	stdLib  *Std
	stdMu   sync.Mutex
)

func std() *Std {
	stdOnce.Do(func() { stdLib = NewStd() })
	return stdLib
}

// harness drives a chip's inputs from switches and reads its outputs.
type harness struct {
	t    *testing.T
	chip *Chip
	s    *sim.Sim
	nl   *sim.Netlist
}

func newHarness(t *testing.T, c *Chip) *harness {
	t.Helper()
	stdMu.Lock()
	defer stdMu.Unlock()
	l := std().Lib
	name := "Test" + c.ID
	if _, ok := l.Chip(name); !ok {
		l.Define(name, func(b *Builder) {
			pins := Pins{}
			for _, p := range c.Inputs {
				if p.Name == "clk" {
					pins["clk"] = Output(b.Device(&circuit.Part{ID: "clock", Kind: circuit.KindClock}, nil), "q", 1)
					continue
				}
				var bus Bus
				for i := range p.Width {
					id := fmt.Sprintf("sw_%s_%d", p.Name, i)
					bus = append(bus, Output(b.Device(&circuit.Part{ID: id, Kind: circuit.KindSwitch}, nil), "q", 1)...)
				}
				pins[p.Name] = bus
			}
			outs := b.UseAs("dut", c, c.ID, pins)
			for _, p := range c.Outputs {
				b.Out("o_"+p.Name, outs[p.Name])
			}
		})
	}
	d := l.Design.Clone()
	d.Root = name
	nl, err := sim.Compile(d)
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return &harness{t: t, chip: c, s: sim.New(nl, sim.ModeEvent), nl: nl}
}

func (h *harness) set(name string, v uint64) {
	for _, p := range h.chip.Inputs {
		if p.Name == name {
			for i := range p.Width {
				if err := h.s.Poke(fmt.Sprintf("sw_%s_%d", name, i), uint8(v>>uint(i)&1)); err != nil {
					h.t.Fatal(err)
				}
			}
			return
		}
	}
	h.t.Fatalf("%s has no input %q", h.chip.ID, name)
}

func (h *harness) settle() {
	if err := h.s.Propagate(); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) get(name string) uint64 { return h.s.Bits(h.nl.Root().Pin("dut", name)) }

// exhaustive checks every input combination against a reference model.
func exhaustive(t *testing.T, c *Chip, ref func(in []uint64) []uint64) {
	h := newHarness(t, c)
	total := 0
	for _, p := range c.Inputs {
		total += p.Width
	}
	for combo := uint64(0); combo < 1<<uint(total); combo++ {
		in := make([]uint64, len(c.Inputs))
		shift := 0
		for i, p := range c.Inputs {
			in[i] = combo >> uint(shift) & (1<<uint(p.Width) - 1)
			h.set(p.Name, in[i])
			shift += p.Width
		}
		h.settle()
		want := ref(in)
		for i, p := range c.Outputs {
			if got := h.get(p.Name); got != want[i] {
				t.Fatalf("%s%v: output %s = %#x, want %#x", c.ID, in, p.Name, got, want[i])
			}
		}
	}
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func TestTruthTables(t *testing.T) {
	s := std()
	m8 := uint64(0xFF)
	cases := []struct {
		chip *Chip
		ref  func(in []uint64) []uint64
	}{
		{s.Not, func(in []uint64) []uint64 { return []uint64{in[0] ^ 1} }},
		{s.And, func(in []uint64) []uint64 { return []uint64{in[0] & in[1]} }},
		{s.Or, func(in []uint64) []uint64 { return []uint64{in[0] | in[1]} }},
		{s.Xor, func(in []uint64) []uint64 { return []uint64{in[0] ^ in[1]} }},
		{s.Mux, func(in []uint64) []uint64 { return []uint64{[]uint64{in[0], in[1]}[in[2]]} }},
		{s.DMux, func(in []uint64) []uint64 { return []uint64{in[0] & (in[1] ^ 1), in[0] & in[1]} }},
		{s.Mux4Way, func(in []uint64) []uint64 { return []uint64{in[in[4]]} }},
		{s.DMux4Way, func(in []uint64) []uint64 {
			out := make([]uint64, 4)
			out[in[1]] = in[0]
			return out
		}},
		{s.HalfAdder, func(in []uint64) []uint64 { return []uint64{in[0] ^ in[1], in[0] & in[1]} }},
		{s.FullAdder, func(in []uint64) []uint64 {
			sum := in[0] + in[1] + in[2]
			return []uint64{sum & 1, sum >> 1}
		}},
		{s.Not8, func(in []uint64) []uint64 { return []uint64{^in[0] & m8} }},
		{s.Or8Way, func(in []uint64) []uint64 { return []uint64{b2u(in[0] != 0)} }},
		{s.Inc8, func(in []uint64) []uint64 { return []uint64{(in[0] + 1) & m8} }},
		{s.Decoder2to4, func(in []uint64) []uint64 { return []uint64{1 << in[0]} }},
		{s.Decoder4to16, func(in []uint64) []uint64 { return []uint64{1 << in[0]} }},
		{s.And8, func(in []uint64) []uint64 { return []uint64{in[0] & in[1]} }},
		{s.Or8, func(in []uint64) []uint64 { return []uint64{in[0] | in[1]} }},
		{s.Xor8, func(in []uint64) []uint64 { return []uint64{in[0] ^ in[1]} }},
		{s.Mux8, func(in []uint64) []uint64 { return []uint64{[]uint64{in[0], in[1]}[in[2]]} }},
		{s.Add8, func(in []uint64) []uint64 {
			sum := in[0] + in[1] + in[2]
			return []uint64{sum & m8, sum >> 8}
		}},
		{s.Hex7Seg, func(in []uint64) []uint64 {
			out := make([]uint64, 7)
			for seg, dark := range segOff {
				out[seg] = 1
				for _, d := range dark {
					if uint64(d) == in[0] {
						out[seg] = 0
					}
				}
			}
			return out
		}},
	}
	for _, c := range cases {
		t.Run(c.chip.ID, func(t *testing.T) { exhaustive(t, c.chip, c.ref) })
	}
}

func TestHex7SegShowsDigitShapes(t *testing.T) {
	// Lit segment counts of 0..F on a standard display.
	want := []int{6, 2, 5, 5, 4, 5, 6, 3, 7, 6, 6, 5, 4, 5, 5, 4}
	h := newHarness(t, std().Hex7Seg)
	for v, n := range want {
		h.set("in", uint64(v))
		h.settle()
		lit := 0
		for _, seg := range circuit.SegmentNames {
			lit += int(h.get(seg))
		}
		if lit != n {
			t.Errorf("digit %X lights %d segments, want %d", v, lit, n)
		}
	}
}

func TestMux4Way8Random(t *testing.T) {
	h := newHarness(t, std().Mux4Way8)
	r := rand.New(rand.NewPCG(1, 2))
	for range 3000 {
		vals := []uint64{r.Uint64N(256), r.Uint64N(256), r.Uint64N(256), r.Uint64N(256)}
		sel := r.Uint64N(4)
		for i, n := range []string{"a", "b", "c", "d"} {
			h.set(n, vals[i])
		}
		h.set("sel", sel)
		h.settle()
		if got := h.get("out"); got != vals[sel] {
			t.Fatalf("sel %d: got %#x want %#x", sel, got, vals[sel])
		}
	}
}

func checkALU(t *testing.T, h *harness, op, a, b uint8, cin bool) {
	h.set("a", uint64(a))
	h.set("b", uint64(b))
	h.set("cin", b2u(cin))
	h.set("op", uint64(op))
	h.settle()
	y, z, n, c := isa.ALU(op, a, b, cin)
	got := []uint64{h.get("y"), h.get("z"), h.get("n"), h.get("c")}
	want := []uint64{uint64(y), b2u(z), b2u(n), b2u(c)}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ALU op=%x a=%#x b=%#x cin=%v: got y,z,n,c=%v want %v", op, a, b, cin, got, want)
		}
	}
}

// TestALU8MatchesReferenceModel is a property test: for every function code
// and carry-in, edge-case operands plus random ones must match isa.ALU.
func TestALU8MatchesReferenceModel(t *testing.T) {
	h := newHarness(t, std().ALU8)
	edges := []uint8{0, 1, 2, 0x7F, 0x80, 0x81, 0xFE, 0xFF}
	r := rand.New(rand.NewPCG(3, 4))
	for op := range uint8(16) {
		for _, cin := range []bool{false, true} {
			for _, a := range edges {
				for _, b := range edges {
					checkALU(t, h, op, a, b, cin)
				}
			}
			for range 2000 {
				checkALU(t, h, op, uint8(r.UintN(256)), uint8(r.UintN(256)), cin)
			}
		}
	}
}

func FuzzALU8(f *testing.F) {
	f.Add(uint8(0), uint8(200), uint8(100), false)
	f.Add(uint8(3), uint8(0), uint8(1), true)
	h := (*harness)(nil)
	f.Fuzz(func(t *testing.T, op, a, b uint8, cin bool) {
		if h == nil {
			h = newHarness(t, std().ALU8)
		}
		h.t = t
		checkALU(t, h, op&15, a, b, cin)
	})
}

func TestRegisterAndPC(t *testing.T) {
	s := std()
	reg := newHarness(t, s.Register8)
	tick := func(h *harness) {
		if err := h.s.Cycle(); err != nil {
			t.Fatal(err)
		}
	}
	reg.set("in", 0x5A)
	reg.set("load", 1)
	tick(reg)
	reg.set("in", 0x33)
	reg.set("load", 0)
	tick(reg)
	if got := reg.get("out"); got != 0x5A {
		t.Fatalf("register holds %#x, want 0x5a", got)
	}
	pc := newHarness(t, s.PC8)
	pc.set("inc", 1)
	for range 5 {
		tick(pc)
	}
	if got := pc.get("out"); got != 5 {
		t.Fatalf("pc after 5 increments = %d", got)
	}
	pc.set("in", 0xF0)
	pc.set("load", 1)
	tick(pc)
	if got := pc.get("out"); got != 0xF0 {
		t.Fatalf("pc load = %#x", got)
	}
	pc.set("reset", 1)
	tick(pc)
	if got := pc.get("out"); got != 0 {
		t.Fatalf("pc reset = %#x", got)
	}
}

func TestLFSRMatchesReference(t *testing.T) {
	h := newHarness(t, std().LFSR16)
	want := uint16(0)
	for i := range 2000 {
		if got := uint16(h.get("out")); got != want {
			t.Fatalf("step %d: lfsr %#x want %#x", i, got, want)
		}
		if err := h.s.Cycle(); err != nil {
			t.Fatal(err)
		}
		want = isa.LFSRNext(want)
	}
}

func TestGateCounts(t *testing.T) {
	d := std().Lib.Design.Clone()
	for _, c := range []struct {
		chip  *Chip
		nands int
	}{{std().Not, 1}, {std().Xor, 4}, {std().Mux, 4}, {std().HalfAdder, 5}, {std().FullAdder, 9}, {std().Add8, 72}} {
		d.Root = c.chip.ID
		nl, err := sim.Compile(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := nl.Stats().Nands; got != c.nands {
			t.Errorf("%s uses %d NANDs, want %d", c.chip.ID, got, c.nands)
		}
	}
}
