package sim

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// Mode selects how combinational logic is settled.
type Mode uint8

const (
	// ModeEvent re-evaluates only gates whose inputs changed, sweeping a
	// bitset of dirty gates in level order (the default).
	ModeEvent Mode = iota
	// ModeSweep evaluates every gate once per settle. It is the reference the
	// event-driven mode is verified against.
	ModeSweep
)

// maxDeltas bounds clock-edge cascades within one step (ripple counters need
// a few); exceeding it means a flip-flop is clocking itself.
const maxDeltas = 64

// ErrOscillation is returned when clock edges keep re-triggering each other.
var ErrOscillation = errors.New("sequential oscillation: a flip-flop output keeps re-clocking flip-flops")

type memState struct {
	words   []uint16
	out     uint16
	version uint64
}

// Sim is a running simulation of a Netlist. It is not safe for concurrent use.
type Sim struct {
	nl      *Netlist
	mode    Mode
	val     []uint8
	dirty   []uint64
	lo, hi  int // dirty word range; lo > hi when empty
	domLast []uint8
	trig    []int
	dffNext []uint8
	memNext []memSample
	mems    []memState

	// GateEvals counts NAND evaluations; HalfSteps counts clock half-periods.
	GateEvals uint64
	HalfSteps uint64
}

type memSample struct {
	addr int
	din  uint16
	we   bool
	en   bool
}

// New creates a simulation in its power-on state: every flip-flop and memory
// output is 0, memories hold their initial contents, logic is settled.
func New(nl *Netlist, mode Mode) *Sim {
	s := &Sim{nl: nl, mode: mode}
	s.val = make([]uint8, nl.numSignals)
	s.val[sigOne] = 1
	s.dirty = make([]uint64, (len(nl.gateIn)/2+63)/64)
	s.lo, s.hi = len(s.dirty), -1
	s.domLast = make([]uint8, len(nl.domains))
	s.dffNext = make([]uint8, len(nl.dffQ))
	s.memNext = make([]memSample, len(nl.mems))
	s.mems = make([]memState, len(nl.mems))
	for i, m := range nl.mems {
		s.mems[i].words = make([]uint16, m.size)
		copy(s.mems[i].words, m.init)
	}
	s.resettle()
	return s
}

// Netlist returns the compiled netlist being simulated.
func (s *Sim) Netlist() *Netlist { return s.nl }

// Mode returns the settle strategy.
func (s *Sim) Mode() Mode { return s.mode }

// Value returns the current value (0 or 1) of a signal.
func (s *Sim) Value(sig int32) uint8 { return s.val[sig] }

// Bits packs a list of signals (LSB first) into an integer.
func (s *Sim) Bits(sigs []int32) uint64 {
	var v uint64
	for i, sig := range sigs {
		v |= uint64(s.val[sig]) << uint(i)
	}
	return v
}

// resettle evaluates every gate and re-arms edge detection without firing
// edges; used at power-on and after state is imported.
func (s *Sim) resettle() {
	s.sweep()
	for i := range s.dirty {
		s.dirty[i] = 0
	}
	s.lo, s.hi = len(s.dirty), -1
	for d, dm := range s.nl.domains {
		s.domLast[d] = s.val[dm.clk]
	}
}

// set drives a source signal and marks its fanout dirty.
func (s *Sim) set(sig int32, v uint8) {
	if s.val[sig] == v {
		return
	}
	s.val[sig] = v
	nl := s.nl
	for _, g := range nl.foGate[nl.foOff[sig]:nl.foOff[sig+1]] {
		w := int(g >> 6)
		s.dirty[w] |= 1 << (uint(g) & 63)
		s.lo = min(s.lo, w)
		s.hi = max(s.hi, w)
	}
}

func (s *Sim) settle() {
	if s.mode == ModeSweep {
		s.sweep()
		for i := s.lo; i <= s.hi; i++ {
			s.dirty[i] = 0
		}
		s.lo, s.hi = len(s.dirty), -1
		return
	}
	s.settleEvent()
}

// sweep evaluates every gate in level order: one pass settles everything.
func (s *Sim) sweep() {
	in, val := s.nl.gateIn, s.val
	base := int(s.nl.gateBase)
	n := len(in) / 2
	for g := range n {
		val[base+g] = 1 ^ (val[in[2*g]] & val[in[2*g+1]])
	}
	s.GateEvals += uint64(n)
}

// settleEvent processes dirty gates in index order. Gates are sorted by level
// and a gate's fanout always has a strictly higher level, so newly dirtied
// gates are always ahead of the cursor and each gate is evaluated at most once.
func (s *Sim) settleEvent() {
	nl := s.nl
	in, val, dirty := nl.gateIn, s.val, s.dirty
	foOff, foGate := nl.foOff, nl.foGate
	base := nl.gateBase
	evals := uint64(0)
	hi := s.hi
	for w := s.lo; w <= hi; w++ {
		for dirty[w] != 0 {
			word := dirty[w]
			g := int32(w<<6 | bits.TrailingZeros64(word))
			dirty[w] = word & (word - 1)
			v := 1 ^ (val[in[2*g]] & val[in[2*g+1]])
			evals++
			o := base + g
			if val[o] == v {
				continue
			}
			val[o] = v
			for _, f := range foGate[foOff[o]:foOff[o+1]] {
				fw := int(f >> 6)
				dirty[fw] |= 1 << (uint(f) & 63)
				if fw > hi {
					hi = fw
				}
			}
		}
	}
	s.lo, s.hi = len(dirty), -1
	s.GateEvals += evals
}

// Propagate settles logic and then fires clock edges until quiescent: all
// domains whose clock rose sample their inputs first, then commit together,
// which gives shift registers and ripple counters their textbook behaviour.
func (s *Sim) Propagate() error {
	s.settle()
	for iter := 0; ; iter++ {
		s.trig = s.trig[:0]
		for d, dm := range s.nl.domains {
			v := s.val[dm.clk]
			if v == 1 && s.domLast[d] == 0 {
				s.trig = append(s.trig, d)
			}
			s.domLast[d] = v
		}
		if len(s.trig) == 0 {
			return nil
		}
		if iter >= maxDeltas {
			return ErrOscillation
		}
		for _, d := range s.trig {
			dm := &s.nl.domains[d]
			for _, i := range dm.dffs {
				s.dffNext[i] = s.val[s.nl.dffD[i]]
			}
			for _, i := range dm.mems {
				s.sampleMem(int(i))
			}
		}
		for _, d := range s.trig {
			dm := &s.nl.domains[d]
			for _, i := range dm.dffs {
				s.set(s.nl.dffQ[i], s.dffNext[i])
			}
			for _, i := range dm.mems {
				s.commitMem(int(i))
			}
		}
		s.settle()
	}
}

func (s *Sim) sampleMem(i int) {
	m := &s.nl.mems[i]
	smp := memSample{addr: int(s.Bits(m.addr)), we: s.val[m.we] == 1, en: s.val[m.en] == 1}
	if m.din != nil {
		smp.din = uint16(s.Bits(m.din))
	}
	s.memNext[i] = smp
}

// commitMem applies a sampled edge: read-before-write, registered output.
func (s *Sim) commitMem(i int) {
	m, st, smp := &s.nl.mems[i], &s.mems[i], s.memNext[i]
	if !smp.en {
		return
	}
	st.out = st.words[smp.addr]
	if smp.we && len(m.din) > 0 { // ROMs have no data input
		if st.words[smp.addr] != smp.din&m.dataMask {
			st.words[smp.addr] = smp.din & m.dataMask
			st.version++
		}
	}
	for b, sig := range m.dout {
		s.set(sig, uint8(st.out>>uint(b)&1))
	}
}

// HalfStep toggles every clock source and propagates. Two half-steps make
// one clock cycle (one rising edge).
func (s *Sim) HalfStep() error {
	for _, c := range s.nl.clocks {
		s.set(c, 1^s.val[c])
	}
	s.HalfSteps++
	return s.Propagate()
}

// Cycle runs one full clock period (low-to-high-to-low or the reverse).
func (s *Sim) Cycle() error {
	if err := s.HalfStep(); err != nil {
		return err
	}
	return s.HalfStep()
}

// Poke sets an external source (switch, button, joystick line) without
// propagating; call Propagate after a batch of pokes.
func (s *Sim) Poke(key string, v uint8) error {
	i, ok := s.nl.sourceIdx[key]
	if !ok {
		return fmt.Errorf("no input source %q", key)
	}
	s.set(s.nl.sources[i].Signal, v&1)
	return nil
}

// Memory returns the live contents of the RAM/ROM/matrix with the given key
// and a version that increments on every write.
func (s *Sim) Memory(key string) ([]uint16, uint64, bool) {
	i, ok := s.nl.memIdx[key]
	if !ok {
		return nil, 0, false
	}
	return s.mems[i].words, s.mems[i].version, true
}

// State is the sequential state of a simulation keyed by stable part paths,
// so it survives recompiling an edited design.
type State struct {
	DFFs    map[string]uint8
	Sources map[string]uint8
	Mems    map[string][]uint16
	MemOut  map[string]uint16
}

// Export captures flip-flop, input and memory state.
func (s *Sim) Export() State {
	st := State{DFFs: map[string]uint8{}, Sources: map[string]uint8{}, Mems: map[string][]uint16{}, MemOut: map[string]uint16{}}
	for i, k := range s.nl.dffKey {
		st.DFFs[k] = s.val[s.nl.dffQ[i]]
	}
	for _, src := range s.nl.sources {
		st.Sources[src.Key] = s.val[src.Signal]
	}
	for i, m := range s.nl.mems {
		if m.kind != circuit.KindROM {
			st.Mems[m.key] = append([]uint16(nil), s.mems[i].words...)
		}
		st.MemOut[m.key] = s.mems[i].out
	}
	return st
}

// Import restores matching state from Export (missing keys keep power-on
// values) and re-settles without firing clock edges.
func (s *Sim) Import(st State) {
	for i, k := range s.nl.dffKey {
		if v, ok := st.DFFs[k]; ok {
			s.val[s.nl.dffQ[i]] = v
		}
	}
	for _, src := range s.nl.sources {
		if v, ok := st.Sources[src.Key]; ok {
			s.val[src.Signal] = v
		}
	}
	for i, m := range s.nl.mems {
		if words, ok := st.Mems[m.key]; ok && len(words) == len(s.mems[i].words) {
			copy(s.mems[i].words, words)
			s.mems[i].version++
		}
		if out, ok := st.MemOut[m.key]; ok {
			s.mems[i].out = out & m.dataMask
			for b, sig := range m.dout {
				s.val[sig] = uint8(s.mems[i].out >> uint(b) & 1)
			}
		}
	}
	s.resettle()
}
