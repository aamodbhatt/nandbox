// Package sim compiles a hierarchical circuit design into a flat, levelized
// NAND netlist and simulates it.
//
// The combinational network contains only 2-input NAND gates. Everything that
// holds state (DFFs, RAM, ROM, the LED matrix) is synchronous: it samples its
// inputs on the rising edge of its clock net and drives "source" signals. That
// split is what makes levelized, event-driven evaluation exact: within a settle,
// every gate is evaluated at most once, in level order.
package sim

import (
	"strings"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// Signal indices 0 and 1 are the constant nets.
const (
	sigZero int32 = 0
	sigOne  int32 = 1
)

// SourceKind says what drives an externally controlled source signal.
type SourceKind uint8

// Source kinds.
const (
	SourceSwitch SourceKind = iota
	SourceButton
	SourceJoystick
	SourceClock
)

// Source is a signal driven from outside the logic: a switch, button,
// joystick line or clock. Key is stable across recompiles.
type Source struct {
	Key    string
	Kind   SourceKind
	Signal int32
	Inst   *Instance
	Part   string
	Pin    string
}

// memSpec is a compiled synchronous memory (RAM, ROM or LED matrix).
type memSpec struct {
	kind     circuit.Kind
	key      string
	inst     *Instance
	part     string
	addr     []int32
	din      []int32
	dout     []int32
	we, en   int32
	clk      int32
	size     int
	dataMask uint16
	init     []uint16
}

// domain groups the sequential elements clocked by the same net.
type domain struct {
	clk  int32
	dffs []int32
	mems []int32
}

// Stats summarizes a compiled netlist.
type Stats struct {
	Nands    int `json:"nands"`
	DFFs     int `json:"dffs"`
	Memories int `json:"memories"`
	Signals  int `json:"signals"`
	Levels   int `json:"levels"`
}

// Netlist is the immutable result of compiling a design. Gates are stored as
// struct-of-arrays sorted by level; gate g drives signal gateBase+g.
type Netlist struct {
	numSignals int
	gateBase   int32
	gateIn     []int32 // inputs of gate g at [2g] and [2g+1]
	foOff      []int32 // fanout of signal s: foGate[foOff[s]:foOff[s+1]]
	foGate     []int32
	dffD       []int32
	dffQ       []int32
	dffKey     []string
	domains    []domain
	mems       []memSpec
	clocks     []int32
	sources    []Source
	sourceIdx  map[string]int
	memIdx     map[string]int
	root       *Instance
	stats      Stats
}

// Stats returns gate, flip-flop and memory counts.
func (n *Netlist) Stats() Stats { return n.stats }

// Root returns the instance tree, used to map editor objects to signals.
func (n *Netlist) Root() *Instance { return n.root }

// Sources lists every externally driven signal.
func (n *Netlist) Sources() []Source { return n.sources }

// Instance is one placed copy of a chip definition in the flattened design.
// It maps every pin of every part inside it to signal indices, which lets the
// editor show live values for whatever instance the user is looking at.
type Instance struct {
	Path     []string // part IDs from the root
	Def      string
	Children map[string]*Instance
	pins     map[string]map[string][]int32
}

// Key returns a stable textual key for a part inside this instance.
func (in *Instance) Key(part string) string {
	if len(in.Path) == 0 {
		return part
	}
	return strings.Join(in.Path, "/") + "/" + part
}

// Pin returns the signals of one pin of one part (LSB first), or nil.
func (in *Instance) Pin(part, pin string) []int32 { return in.pins[part][pin] }

// Find walks a path of part IDs down from this instance.
func (in *Instance) Find(path []string) *Instance {
	cur := in
	for _, id := range path {
		next, ok := cur.Children[id]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}
