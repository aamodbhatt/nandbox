package sim

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// bench is a tiny design builder for engine tests (package hdl is the real DSL).
type bench struct {
	c *circuit.ChipDef
	n int
}

func newBench() *bench { return &bench{c: circuit.NewChipDef("world", "World")} }

func (b *bench) add(kind circuit.Kind, mods ...func(*circuit.Part)) string {
	b.n++
	id := fmt.Sprintf("%s%d", kind, b.n)
	p := &circuit.Part{ID: id, Kind: kind}
	for _, m := range mods {
		m(p)
	}
	b.c.Parts[id] = p
	return id
}

func (b *bench) wire(from, fromPin, to, toPin string) {
	b.n++
	id := fmt.Sprintf("w%d", b.n)
	b.c.Wires[id] = &circuit.Wire{ID: id, From: circuit.PinRef{Part: from, Pin: fromPin}, To: circuit.PinRef{Part: to, Pin: toPin}}
}

func (b *bench) design() *circuit.Design {
	return &circuit.Design{Root: "world", Chips: map[string]*circuit.ChipDef{"world": b.c}}
}

func mustCompile(t *testing.T, d *circuit.Design) *Netlist {
	t.Helper()
	nl, err := Compile(d)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return nl
}

func TestNandTruthTable(t *testing.T) {
	b := newBench()
	a, c := b.add(circuit.KindSwitch), b.add(circuit.KindSwitch)
	g := b.add(circuit.KindNand)
	b.wire(a, "q", g, "a")
	b.wire(c, "q", g, "b")
	nl := mustCompile(t, b.design())
	for _, mode := range []Mode{ModeEvent, ModeSweep} {
		s := New(nl, mode)
		for x := range 4 {
			_ = s.Poke(a, uint8(x&1))
			_ = s.Poke(c, uint8(x>>1))
			if err := s.Propagate(); err != nil {
				t.Fatal(err)
			}
			want := uint8(1)
			if x == 3 {
				want = 0
			}
			if got := s.Value(nl.Root().Pin(g, "y")[0]); got != want {
				t.Fatalf("mode %d: NAND(%d,%d)=%d, want %d", mode, x&1, x>>1, got, want)
			}
		}
	}
}

func TestCombinationalLoopIsReportedWithItsGates(t *testing.T) {
	b := newBench()
	g1, g2, g3 := b.add(circuit.KindNand), b.add(circuit.KindNand), b.add(circuit.KindNand)
	b.wire(g1, "y", g2, "a")
	b.wire(g2, "y", g3, "a")
	b.wire(g3, "y", g1, "a")
	_, err := Compile(b.design())
	var ce *CompileError
	if !errors.As(err, &ce) || ce.Kind != "loop" {
		t.Fatalf("want loop error, got %v", err)
	}
	if len(ce.Locations) != 3 {
		t.Fatalf("loop should name 3 gates, got %+v", ce.Locations)
	}
	for _, g := range []string{g1, g2, g3} {
		if !strings.Contains(ce.Msg, g) {
			t.Errorf("message %q does not mention %s", ce.Msg, g)
		}
	}
}

func TestMultipleDriversRejected(t *testing.T) {
	b := newBench()
	g1, g2, led := b.add(circuit.KindNand), b.add(circuit.KindNand), b.add(circuit.KindLED)
	b.wire(g1, "y", led, "a")
	b.wire(g2, "y", led, "a")
	_, err := Compile(b.design())
	var ce *CompileError
	if !errors.As(err, &ce) || ce.Kind != "drivers" || len(ce.Locations) != 2 {
		t.Fatalf("want drivers error naming 2 parts, got %v", err)
	}
}

func TestSplitterAndChipBoundariesJoinNets(t *testing.T) {
	inv := circuit.NewChipDef("inv", "Inv")
	inv.Parts["i"] = &circuit.Part{ID: "i", Kind: circuit.KindIn, Width: 1}
	inv.Parts["o"] = &circuit.Part{ID: "o", Kind: circuit.KindOut, Width: 1, Y: 0}
	inv.Parts["n"] = &circuit.Part{ID: "n", Kind: circuit.KindNand}
	inv.Wires["w1"] = &circuit.Wire{ID: "w1", From: circuit.PinRef{Part: "i", Pin: "q"}, To: circuit.PinRef{Part: "n", Pin: "a"}}
	inv.Wires["w2"] = &circuit.Wire{ID: "w2", From: circuit.PinRef{Part: "i", Pin: "q"}, To: circuit.PinRef{Part: "n", Pin: "b"}}
	inv.Wires["w3"] = &circuit.Wire{ID: "w3", From: circuit.PinRef{Part: "n", Pin: "y"}, To: circuit.PinRef{Part: "o", Pin: "a"}}
	b := newBench()
	k := b.add(circuit.KindConst, func(p *circuit.Part) { p.Width, p.Value = 4, 0b0101 })
	sp := b.add(circuit.KindSplitter, func(p *circuit.Part) { p.Width = 4; p.Taps = []circuit.Tap{{Lo: 2, Hi: 3}} })
	u := b.add(circuit.KindChip, func(p *circuit.Part) { p.Chip = "inv" })
	b.wire(k, "q", sp, "bus")
	b.wire(sp, "t0", u, "i")
	d := b.design()
	d.Chips["inv"] = inv
	nl := mustCompile(t, d)
	s := New(nl, ModeEvent)
	if got := s.Value(nl.Root().Pin(u, "o")[0]); got != 0 {
		t.Fatalf("inverter of bit 2 of 0101 = %d, want 0", got)
	}
	if nl.Stats().Nands != 1 {
		t.Fatalf("want 1 NAND, got %+v", nl.Stats())
	}
}

func TestShiftRegisterSamplesBeforeCommit(t *testing.T) {
	b := newBench()
	clk, sw := b.add(circuit.KindClock), b.add(circuit.KindSwitch)
	var ffs []string
	prev, prevPin := sw, "q"
	for range 4 {
		f := b.add(circuit.KindDFF)
		b.wire(prev, prevPin, f, "d")
		b.wire(clk, "q", f, "clk")
		ffs = append(ffs, f)
		prev, prevPin = f, "q"
	}
	nl := mustCompile(t, b.design())
	s := New(nl, ModeEvent)
	_ = s.Poke(sw, 1)
	if err := s.Cycle(); err != nil {
		t.Fatal(err)
	}
	_ = s.Poke(sw, 0)
	for cycle := 1; cycle <= 4; cycle++ {
		for i, f := range ffs {
			want := uint8(0)
			if i == cycle-1 {
				want = 1
			}
			if got := s.Value(nl.Root().Pin(f, "q")[0]); got != want {
				t.Fatalf("after %d cycles ff%d=%d want %d", cycle, i, got, want)
			}
		}
		if err := s.Cycle(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRippleCounterUsesDeltaEdges(t *testing.T) {
	// Each stage's inverted output clocks the next: a 3-bit ripple counter.
	b := newBench()
	clk := b.add(circuit.KindClock)
	var qs []string
	src := clk
	for range 3 {
		f, n := b.add(circuit.KindDFF), b.add(circuit.KindNand)
		b.wire(f, "q", n, "a")
		b.wire(f, "q", n, "b")
		b.wire(n, "y", f, "d")
		b.wire(src, map[bool]string{true: "q", false: "y"}[src == clk], f, "clk")
		qs = append(qs, f)
		src = n
	}
	nl := mustCompile(t, b.design())
	s := New(nl, ModeEvent)
	for want := 1; want <= 10; want++ {
		if err := s.Cycle(); err != nil {
			t.Fatal(err)
		}
		got := 0
		for i, q := range qs {
			got |= int(s.Value(nl.Root().Pin(q, "q")[0])) << i
		}
		if got != want%8 {
			t.Fatalf("cycle %d: counter=%d want %d", want, got, want%8)
		}
	}
}

func TestRAMReadBeforeWriteAndStateCarryOver(t *testing.T) {
	b := newBench()
	clk, we := b.add(circuit.KindClock), b.add(circuit.KindSwitch)
	addr := b.add(circuit.KindConst, func(p *circuit.Part) { p.Width, p.Value = 4, 3 })
	din := b.add(circuit.KindConst, func(p *circuit.Part) { p.Width, p.Value = 8, 0xA5 })
	ram := b.add(circuit.KindRAM, func(p *circuit.Part) { p.AddrBits, p.DataBits, p.Words = 4, 8, []uint16{0, 0, 0, 0x11} })
	b.wire(clk, "q", ram, "clk")
	b.wire(we, "q", ram, "we")
	b.wire(addr, "q", ram, "addr")
	b.wire(din, "q", ram, "din")
	nl := mustCompile(t, b.design())
	s := New(nl, ModeEvent)
	_ = s.Poke(we, 1)
	_ = s.Cycle()
	out := nl.Root().Pin(ram, "dout")
	if got := s.Bits(out); got != 0x11 {
		t.Fatalf("first edge should read old 0x11, got %#x", got)
	}
	_ = s.Cycle()
	if got := s.Bits(out); got != 0xA5 {
		t.Fatalf("second edge should read written 0xA5, got %#x", got)
	}
	s2 := New(mustCompile(t, b.design()), ModeEvent)
	s2.Import(s.Export())
	if got := s2.Bits(out); got != 0xA5 {
		t.Fatalf("state carry-over lost RAM output: %#x", got)
	}
	words, _, _ := s2.Memory(ram)
	if words[3] != 0xA5 {
		t.Fatalf("state carry-over lost RAM contents: %#x", words[3])
	}
}

// randomCircuit builds an acyclic NAND network with DFF feedback, clocked by
// one clock and a switch-driven secondary clock (to exercise multi-domain edges).
func randomCircuit(r *rand.Rand) (*circuit.Design, []string) {
	b := newBench()
	clk := b.add(circuit.KindClock)
	type out struct{ part, pin string }
	var sigs []out
	var switches []string
	for range 2 + r.IntN(5) {
		sw := b.add(circuit.KindSwitch)
		switches = append(switches, sw)
		sigs = append(sigs, out{sw, "q"})
	}
	var dffs []string
	for range r.IntN(6) {
		f := b.add(circuit.KindDFF)
		dffs = append(dffs, f)
		sigs = append(sigs, out{f, "q"})
	}
	for range 5 + r.IntN(60) {
		g := b.add(circuit.KindNand)
		x, y := sigs[r.IntN(len(sigs))], sigs[r.IntN(len(sigs))]
		b.wire(x.part, x.pin, g, "a")
		b.wire(y.part, y.pin, g, "b")
		sigs = append(sigs, out{g, "y"})
	}
	for _, f := range dffs {
		x := sigs[r.IntN(len(sigs))]
		b.wire(x.part, x.pin, f, "d")
		if r.IntN(4) == 0 {
			sw := switches[r.IntN(len(switches))]
			b.wire(sw, "q", f, "clk")
		} else {
			b.wire(clk, "q", f, "clk")
		}
	}
	return b.design(), switches
}

func checkEquivalence(t *testing.T, seed uint64) {
	r := rand.New(rand.NewPCG(seed, 7))
	d, switches := randomCircuit(r)
	nl := mustCompile(t, d)
	ev, sw := New(nl, ModeEvent), New(nl, ModeSweep)
	for step := range 60 {
		if r.IntN(3) == 0 {
			key, v := switches[r.IntN(len(switches))], uint8(r.IntN(2))
			_ = ev.Poke(key, v)
			_ = sw.Poke(key, v)
			if e1, e2 := ev.Propagate(), sw.Propagate(); (e1 == nil) != (e2 == nil) {
				t.Fatalf("seed %d step %d: errors differ: %v vs %v", seed, step, e1, e2)
			}
		} else if e1, e2 := ev.HalfStep(), sw.HalfStep(); (e1 == nil) != (e2 == nil) {
			t.Fatalf("seed %d step %d: errors differ: %v vs %v", seed, step, e1, e2)
		}
		for i := range ev.val {
			if ev.val[i] != sw.val[i] {
				t.Fatalf("seed %d step %d: signal %d event=%d sweep=%d", seed, step, i, ev.val[i], sw.val[i])
			}
		}
	}
	if ev.GateEvals > sw.GateEvals {
		t.Errorf("seed %d: event mode did more work (%d) than full sweeps (%d)", seed, ev.GateEvals, sw.GateEvals)
	}
}

func TestEventDrivenMatchesFullSweep(t *testing.T) {
	for seed := range uint64(400) {
		checkEquivalence(t, seed)
	}
}

func FuzzEventDrivenMatchesFullSweep(f *testing.F) {
	for _, s := range []uint64{1, 42, 9001} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed uint64) { checkEquivalence(t, seed) })
}
