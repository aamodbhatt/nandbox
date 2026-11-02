package sim

import (
	"testing"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// The DFF is a simulator primitive for speed. This test proves it behaves
// exactly like the textbook positive-edge master-slave flip-flop built from
// nine NAND gates, which has feedback loops and therefore needs a different
// simulator: a gate-at-a-time relaxation that iterates until the nets stop moving.

// Net indices of the reference circuit.
const (
	nD = iota
	nClk
	nNclk
	nM1
	nM2
	nMQ
	nMQn
	nS1
	nS2
	nQ
	nQn
	nNets
)

// masterSlave lists each NAND as (output, inputA, inputB).
var masterSlave = [][3]int{
	{nNclk, nClk, nClk}, // master is transparent while the clock is low
	{nM1, nD, nNclk},
	{nM2, nM1, nNclk},
	{nMQ, nM1, nMQn},
	{nMQn, nM2, nMQ},
	{nS1, nMQ, nClk}, // slave is transparent while the clock is high
	{nS2, nS1, nClk},
	{nQ, nS1, nQn},
	{nQn, nS2, nQ},
}

// relax fires one gate at a time (in-place, asynchronous updates) until no
// gate wants to change. Updating one gate at a time models distinct gate
// delays and avoids the artificial oscillation that lock-step updates create
// in cross-coupled latches.
func relax(t *testing.T, v *[nNets]uint8) {
	t.Helper()
	for range 50 {
		changed := false
		for _, g := range masterSlave {
			if n := 1 ^ (v[g[1]] & v[g[2]]); v[g[0]] != n {
				v[g[0]], changed = n, true
			}
		}
		if !changed {
			return
		}
	}
	t.Fatal("master-slave reference did not settle")
}

// initState sets the latches to hold stored bit q under inputs (d, clk).
func initState(t *testing.T, d, clk, q uint8) [nNets]uint8 {
	var v [nNets]uint8
	v[nD], v[nClk] = d, clk
	v[nMQ], v[nMQn], v[nQ], v[nQn] = q, 1-q, q, 1-q
	if clk == 0 {
		v[nMQ], v[nMQn] = d, 1-d // master is transparent
	}
	relax(t, &v)
	if v[nQ] != q || v[nQn] != 1-q {
		t.Fatalf("reference failed to hold q=%d", q)
	}
	return v
}

func TestDFFPrimitiveMatchesNandMasterSlave(t *testing.T) {
	b := newBench()
	dSw, cSw := b.add(circuit.KindSwitch), b.add(circuit.KindSwitch)
	ff := b.add(circuit.KindDFF)
	b.wire(dSw, "q", ff, "d")
	b.wire(cSw, "q", ff, "clk")
	nl := mustCompile(t, b.design())
	qSig := nl.Root().Pin(ff, "q")[0]

	const depth = 12 // every sequence of 12 single-input changes: 4096 per start state
	runs := 0
	for _, mode := range []Mode{ModeEvent, ModeSweep} {
		for start := range 8 {
			d0, c0, q0 := uint8(start&1), uint8(start>>1&1), uint8(start>>2)
			for seq := range 1 << depth {
				ref := initState(t, d0, c0, q0)
				s := New(nl, mode)
				s.Import(State{Sources: map[string]uint8{dSw: d0, cSw: c0}, DFFs: map[string]uint8{ff: q0}})
				for step := range depth {
					// Fundamental mode: exactly one input changes, then everything settles.
					net, key := nD, dSw
					if seq>>step&1 == 1 {
						net, key = nClk, cSw
					}
					ref[net] ^= 1
					relax(t, &ref)
					if err := s.Poke(key, ref[net]); err != nil {
						t.Fatal(err)
					}
					if err := s.Propagate(); err != nil {
						t.Fatal(err)
					}
					if got := s.Value(qSig); got != ref[nQ] {
						t.Fatalf("mode %d start(d=%d clk=%d q=%d) seq %b step %d: primitive q=%d, NAND master-slave q=%d",
							mode, d0, c0, q0, seq, step, got, ref[nQ])
					}
				}
				runs++
			}
		}
	}
	t.Logf("verified %d runs: %d input sequences (%d steps each) on both engines", runs, runs/2, depth)
}
