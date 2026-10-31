// Package circuit defines nandbox's document model: a design is a set of
// hierarchical chip definitions, each made of placed parts joined by wires.
//
// It is a pure data model. Turning a design into something that can be
// simulated (flattening, net resolution, levelization) lives in package sim.
package circuit

import (
	"fmt"
	"sort"
)

// Kind identifies the type of a part.
type Kind string

// Part kinds. Only NAND, DFF, RAM, ROM and Matrix compute or store anything;
// the rest are wiring (In, Out, Splitter, Const, Chip) or I/O endpoints.
const (
	KindNand     Kind = "nand"
	KindDFF      Kind = "dff"
	KindRAM      Kind = "ram"
	KindROM      Kind = "rom"
	KindMatrix   Kind = "matrix"
	KindClock    Kind = "clock"
	KindSwitch   Kind = "switch"
	KindButton   Kind = "button"
	KindJoystick Kind = "joystick"
	KindConst    Kind = "const"
	KindLED      Kind = "led"
	KindSeg7     Kind = "seg7"
	KindSplitter Kind = "splitter"
	KindIn       Kind = "in"
	KindOut      Kind = "out"
	KindChip     Kind = "chip"
	KindNote     Kind = "note"
)

// Limits shared by validation, the simulator and the editor.
const (
	MaxWidth    = 16 // widest bus a pin may carry
	MaxAddrBits = 10 // largest RAM/ROM is 1024 words
	MatrixSide  = 16 // the LED matrix is MatrixSide x MatrixSide pixels
)

// Point is a position on the editor grid, in grid units.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Tap is one output group of a splitter: bus bits [Lo, Hi).
type Tap struct {
	Lo int `json:"lo"`
	Hi int `json:"hi"`
}

// Part is one placed element inside a chip definition. Fields that do not
// apply to a kind must be zero; Validate enforces this.
type Part struct {
	ID       string   `json:"id"`
	Kind     Kind     `json:"kind"`
	X        int      `json:"x"`
	Y        int      `json:"y"`
	Label    string   `json:"label,omitempty"`
	Chip     string   `json:"chip,omitempty"`     // KindChip: instantiated definition ID
	Width    int      `json:"width,omitempty"`    // in/out/const/splitter bus width
	Value    uint64   `json:"value,omitempty"`    // const value
	Flip     bool     `json:"flip,omitempty"`     // splitter: taps on the left, bus on the right
	Taps     []Tap    `json:"taps,omitempty"`     // splitter output groups
	AddrBits int      `json:"addrBits,omitempty"` // ram/rom
	DataBits int      `json:"dataBits,omitempty"` // ram/rom
	Words    []uint16 `json:"words,omitempty"`    // rom contents, ram/matrix initial contents
}

// PinRef names one pin of one part inside the same chip definition.
type PinRef struct {
	Part string `json:"part"`
	Pin  string `json:"pin"`
}

func (r PinRef) String() string { return r.Part + "." + r.Pin }

// Wire joins two pins of equal width. Wires form nets: every pin bit joined by
// wires (directly or through splitters and chip boundaries) is one signal.
// From is the driving side by convention; the simulator does not rely on it.
type Wire struct {
	ID    string  `json:"id"`
	From  PinRef  `json:"from"`
	To    PinRef  `json:"to"`
	Route []Point `json:"route,omitempty"` // interior bend points; empty means auto-route
}

// ChipDef is a reusable chip: its interface is the set of In and Out parts it
// contains, ordered top to bottom.
type ChipDef struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Parts map[string]*Part `json:"parts"`
	Wires map[string]*Wire `json:"wires"`
}

// Design is a whole document: chip definitions plus the root ("world") chip.
type Design struct {
	Root  string              `json:"root"`
	Chips map[string]*ChipDef `json:"chips"`
}

// NewChipDef returns an empty chip definition.
func NewChipDef(id, name string) *ChipDef {
	return &ChipDef{ID: id, Name: name, Parts: map[string]*Part{}, Wires: map[string]*Wire{}}
}

// Chip returns the definition with the given ID or an error naming it.
func (d *Design) Chip(id string) (*ChipDef, error) {
	c, ok := d.Chips[id]
	if !ok {
		return nil, fmt.Errorf("unknown chip %q", id)
	}
	return c, nil
}

// SortedPartIDs returns part IDs in a stable order so every consumer
// (compiler, layout, encoders) is deterministic.
func (c *ChipDef) SortedPartIDs() []string { return sortedKeys(c.Parts) }

// SortedWireIDs returns wire IDs in a stable order.
func (c *ChipDef) SortedWireIDs() []string { return sortedKeys(c.Wires) }

// SortedChipIDs returns chip definition IDs in a stable order.
func (d *Design) SortedChipIDs() []string { return sortedKeys(d.Chips) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Clone returns a deep copy of the design.
func (d *Design) Clone() *Design {
	out := &Design{Root: d.Root, Chips: make(map[string]*ChipDef, len(d.Chips))}
	for id, c := range d.Chips {
		out.Chips[id] = c.Clone()
	}
	return out
}

// Clone returns a deep copy of the chip definition.
func (c *ChipDef) Clone() *ChipDef {
	out := NewChipDef(c.ID, c.Name)
	for id, p := range c.Parts {
		out.Parts[id] = p.Clone()
	}
	for id, w := range c.Wires {
		out.Wires[id] = w.Clone()
	}
	return out
}

// Clone returns a deep copy of the part.
func (p *Part) Clone() *Part {
	q := *p
	q.Taps = append([]Tap(nil), p.Taps...)
	q.Words = append([]uint16(nil), p.Words...)
	return &q
}

// Clone returns a deep copy of the wire.
func (w *Wire) Clone() *Wire {
	q := *w
	q.Route = append([]Point(nil), w.Route...)
	return &q
}

// Name returns the display name of a part: its label or, failing that, its ID.
func (p *Part) Name() string {
	if p.Label != "" {
		return p.Label
	}
	return p.ID
}
