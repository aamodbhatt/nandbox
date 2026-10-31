// Package hdl is a small Go DSL for describing chips structurally. Every chip
// it builds reduces to NAND gates and DFFs (plus RAM/ROM/matrix memories and
// I/O endpoints), and is emitted as an ordinary circuit.ChipDef: the editor can
// open, inspect and edit it exactly like a chip a user drew by hand.
package hdl

import (
	"fmt"
	"strconv"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/layout"
)

// Bit is one bit of a signal: bit Idx of output pin Pin of part Part (whose
// pin is Width bits wide), or a constant when Part is empty (Idx is 0 or 1).
type Bit struct {
	Part  string
	Pin   string
	Idx   int
	Width int
}

// Bus is an ordered group of bits, least significant first.
type Bus []Bit

// Zero and One are constant bits.
var (
	Zero = Bit{Idx: 0, Width: 1}
	One  = Bit{Idx: 1, Width: 1}
)

func (b Bit) isConst() bool { return b.Part == "" }

// Slice returns bits [lo, hi) of the bus.
func (b Bus) Slice(lo, hi int) Bus { return b[lo:hi] }

// Bit returns a single bit as a 1-bit bus.
func (b Bus) Bit(i int) Bit { return b[i] }

// Const returns a constant bus of the given width.
func Const(width int, value uint64) Bus {
	out := make(Bus, width)
	for i := range out {
		out[i] = Bit{Idx: int(value >> uint(i) & 1), Width: 1}
	}
	return out
}

// Concat joins buses, the first argument supplying the least significant bits.
func Concat(parts ...Bus) Bus {
	var out Bus
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Port is one declared input or output of a chip.
type Port struct {
	Name  string
	Width int
}

// Chip is a defined chip ready to instantiate.
type Chip struct {
	ID         string
	Inputs     []Port
	Outputs    []Port
	Sequential bool // contains flip-flops or memories
}

// Library accumulates chip definitions into one design.
type Library struct {
	Design *circuit.Design
	chips  map[string]*Chip
}

// NewLibrary returns an empty library whose root will be set by SetRoot.
func NewLibrary() *Library {
	return &Library{Design: &circuit.Design{Chips: map[string]*circuit.ChipDef{}}, chips: map[string]*Chip{}}
}

// Chip returns a previously defined chip by ID.
func (l *Library) Chip(id string) (*Chip, bool) {
	c, ok := l.chips[id]
	return c, ok
}

type tapKey struct {
	src string // "part.pin"
	tap circuit.Tap
}

type pendingWire struct {
	from circuit.PinRef
	tap  *tapKey // set when the source is a splitter tap created at finish
	to   circuit.PinRef
}

// Builder constructs one chip definition.
type link struct {
	bus Bus
	dst circuit.PinRef
}

// fwdPart marks placeholder bits created by Forward.
const fwdPart = "~fwd"

type Builder struct {
	lib     *Library
	def     *circuit.ChipDef
	chip    *Chip
	n       int
	links   []link
	fwd     map[string]Bus
	taps    map[string][]circuit.Tap
	tapW    map[string]int
	srcRef  map[string]circuit.PinRef
	pending []pendingWire
}

// Define builds a chip with fn and adds it (laid out) to the library.
// Builder misuse (unknown pins, width mismatches) is a programming error and panics.
func (l *Library) Define(id string, fn func(b *Builder)) *Chip {
	if _, dup := l.chips[id]; dup {
		panic("hdl: chip " + id + " defined twice")
	}
	b := &Builder{lib: l, def: circuit.NewChipDef(id, id), chip: &Chip{ID: id},
		fwd: map[string]Bus{}, taps: map[string][]circuit.Tap{}, tapW: map[string]int{}, srcRef: map[string]circuit.PinRef{}}
	fn(b)
	b.finish()
	l.Design.Chips[id] = b.def
	if err := layout.Place(l.Design, b.def); err != nil {
		panic(err)
	}
	l.chips[id] = b.chip
	return b.chip
}

func (b *Builder) id(prefix string) string {
	b.n++
	return prefix + strconv.Itoa(b.n)
}

func (b *Builder) addPart(p *circuit.Part) {
	if _, dup := b.def.Parts[p.ID]; dup {
		panic(fmt.Sprintf("hdl: %s: duplicate part ID %q", b.def.ID, p.ID))
	}
	b.def.Parts[p.ID] = p
}

func outBus(part, pin string, width int) Bus {
	out := make(Bus, width)
	for i := range out {
		out[i] = Bit{Part: part, Pin: pin, Idx: i, Width: width}
	}
	return out
}

// In declares an input pin and returns its bits.
func (b *Builder) In(name string, width int) Bus {
	b.addPart(&circuit.Part{ID: name, Kind: circuit.KindIn, Width: width, Label: name, Y: 2 * len(b.chip.Inputs)})
	b.chip.Inputs = append(b.chip.Inputs, Port{name, width})
	return outBus(name, "q", width)
}

// Out declares an output pin driven by bus.
func (b *Builder) Out(name string, bus Bus) {
	b.addPart(&circuit.Part{ID: name, Kind: circuit.KindOut, Width: len(bus), Label: name, Y: 2 * len(b.chip.Outputs)})
	b.chip.Outputs = append(b.chip.Outputs, Port{name, len(bus)})
	b.links = append(b.links, link{bus, circuit.PinRef{Part: name, Pin: "a"}})
}

// Nand adds a NAND gate.
func (b *Builder) Nand(x, y Bit) Bit {
	id := b.id("g")
	b.addPart(&circuit.Part{ID: id, Kind: circuit.KindNand})
	b.links = append(b.links, link{Bus{x}, circuit.PinRef{Part: id, Pin: "a"}})
	b.links = append(b.links, link{Bus{y}, circuit.PinRef{Part: id, Pin: "b"}})
	return Bit{Part: id, Pin: "y", Width: 1}
}

// DFF adds a rising-edge D flip-flop.
func (b *Builder) DFF(d, clk Bit) Bit {
	id := b.id("ff")
	b.addPart(&circuit.Part{ID: id, Kind: circuit.KindDFF})
	b.links = append(b.links, link{Bus{d}, circuit.PinRef{Part: id, Pin: "d"}})
	b.links = append(b.links, link{Bus{clk}, circuit.PinRef{Part: id, Pin: "clk"}})
	b.chip.Sequential = true
	return Bit{Part: id, Pin: "q", Width: 1}
}

// Pins maps port names to buses for instantiating a chip.
type Pins map[string]Bus

// Use instantiates chip with the given inputs and returns its outputs.
func (b *Builder) Use(c *Chip, label string, ins Pins) Pins { return b.UseAs(b.id("u"), c, label, ins) }

// UseAs is Use with an explicit part ID, for instances that other code
// addresses by path (the CPU and I/O bus of the computer).
func (b *Builder) UseAs(id string, c *Chip, label string, ins Pins) Pins {
	b.addPart(&circuit.Part{ID: id, Kind: circuit.KindChip, Chip: c.ID, Label: label})
	for _, p := range c.Inputs {
		bus, ok := ins[p.Name]
		if !ok {
			panic(fmt.Sprintf("hdl: %s: instance of %s missing input %q", b.def.ID, c.ID, p.Name))
		}
		if len(bus) != p.Width {
			panic(fmt.Sprintf("hdl: %s: %s.%s wants %d bits, got %d", b.def.ID, c.ID, p.Name, p.Width, len(bus)))
		}
		b.links = append(b.links, link{bus, circuit.PinRef{Part: id, Pin: p.Name}})
	}
	if len(ins) != len(c.Inputs) {
		panic(fmt.Sprintf("hdl: %s: instance of %s given unknown inputs", b.def.ID, c.ID))
	}
	if c.Sequential {
		b.chip.Sequential = true
	}
	out := Pins{}
	for _, p := range c.Outputs {
		out[p.Name] = outBus(id, p.Name, p.Width)
	}
	return out
}

// Device adds an I/O or memory primitive and wires its inputs; it returns
// the part ID so callers can name its outputs.
func (b *Builder) Device(p *circuit.Part, ins Pins) string {
	if p.ID == "" {
		p.ID = b.id(string(p.Kind))
	}
	b.addPart(p)
	for _, name := range sortedPinNames(ins) {
		b.links = append(b.links, link{ins[name], circuit.PinRef{Part: p.ID, Pin: name}})
	}
	switch p.Kind {
	case circuit.KindRAM, circuit.KindROM, circuit.KindMatrix:
		b.chip.Sequential = true
	}
	return p.ID
}

// Output names an output pin of a part added with Device.
func Output(part, pin string, width int) Bus { return outBus(part, pin, width) }

func sortedPinNames(p Pins) []string {
	names := make([]string, 0, len(p))
	for k := range p {
		names = append(names, k)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// connect wires bus to a destination pin, inserting splitters (bus taps) and
// joiners only where the bits do not already form a whole source pin.
func (b *Builder) connect(bus Bus, dst circuit.PinRef) {
	type run struct{ lo, hi int } // positions within bus
	var runs []run
	for i := 0; i < len(bus); {
		j := i + 1
		for j < len(bus) && sameRun(bus[j-1], bus[j]) {
			j++
		}
		runs = append(runs, run{i, j})
		i = j
	}
	if len(runs) == 1 {
		b.wireFrom(bus, dst)
		return
	}
	jid := b.id("join")
	taps := make([]circuit.Tap, len(runs))
	for i, r := range runs {
		taps[i] = circuit.Tap{Lo: r.lo, Hi: r.hi}
	}
	b.addPart(&circuit.Part{ID: jid, Kind: circuit.KindSplitter, Width: len(bus), Taps: taps, Flip: true})
	for i, r := range runs {
		b.wireFrom(bus[r.lo:r.hi], circuit.PinRef{Part: jid, Pin: "t" + strconv.Itoa(i)})
	}
	b.pending = append(b.pending, pendingWire{from: circuit.PinRef{Part: jid, Pin: "bus"}, to: dst})
}

func sameRun(a, c Bit) bool {
	if a.isConst() || c.isConst() {
		return a.isConst() && c.isConst()
	}
	return a.Part == c.Part && a.Pin == c.Pin && c.Idx == a.Idx+1
}

// wireFrom connects one run (all constant, or consecutive bits of one pin).
func (b *Builder) wireFrom(run Bus, dst circuit.PinRef) {
	if run[0].isConst() {
		var v uint64
		for i, bit := range run {
			v |= uint64(bit.Idx) << uint(i)
		}
		kid := b.id("k")
		b.addPart(&circuit.Part{ID: kid, Kind: circuit.KindConst, Width: len(run), Value: v})
		b.pending = append(b.pending, pendingWire{from: circuit.PinRef{Part: kid, Pin: "q"}, to: dst})
		return
	}
	src := circuit.PinRef{Part: run[0].Part, Pin: run[0].Pin}
	lo, hi, w := run[0].Idx, run[len(run)-1].Idx+1, run[0].Width
	if lo == 0 && hi == w {
		b.pending = append(b.pending, pendingWire{from: src, to: dst})
		return
	}
	key := src.String()
	tap := circuit.Tap{Lo: lo, Hi: hi}
	b.srcRef[key], b.tapW[key] = src, w
	found := false
	for _, t := range b.taps[key] {
		found = found || t == tap
	}
	if !found {
		b.taps[key] = append(b.taps[key], tap)
	}
	b.pending = append(b.pending, pendingWire{tap: &tapKey{key, tap}, to: dst})
}

// Forward returns placeholder bits for a signal that is defined later (the
// feedback paths of registers and of the CPU datapath). Bind it with Resolve.
func (b *Builder) Forward(width int) Bus {
	id := b.id("f")
	return outBus(fwdPart, id, width)
}

// Resolve binds a Forward placeholder to the bus that actually drives it.
func (b *Builder) Resolve(placeholder, bus Bus) {
	if len(placeholder) != len(bus) {
		panic(fmt.Sprintf("hdl: %s: resolving %d-bit forward with %d bits", b.def.ID, len(placeholder), len(bus)))
	}
	b.fwd[placeholder[0].Pin] = bus
}

func (b *Builder) resolveBit(bit Bit) Bit {
	for depth := 0; bit.Part == fwdPart; depth++ {
		bus, ok := b.fwd[bit.Pin]
		if !ok || depth > 64 {
			panic(fmt.Sprintf("hdl: %s: forward signal never resolved", b.def.ID))
		}
		bit = bus[bit.Idx]
	}
	return bit
}

// finish resolves forward signals, wires every link (creating joiners and
// splitters as needed) and emits the wires.
func (b *Builder) finish() {
	for _, l := range b.links {
		bus := make(Bus, len(l.bus))
		for i, bit := range l.bus {
			bus[i] = b.resolveBit(bit)
		}
		b.connect(bus, l.dst)
	}
	splitID := map[string]string{}
	keys := make([]string, 0, len(b.taps))
	for k := range b.taps {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	for _, k := range keys {
		sid := b.id("split")
		b.addPart(&circuit.Part{ID: sid, Kind: circuit.KindSplitter, Width: b.tapW[k], Taps: b.taps[k]})
		splitID[k] = sid
		b.emit(b.srcRef[k], circuit.PinRef{Part: sid, Pin: "bus"})
	}
	for _, pw := range b.pending {
		from := pw.from
		if pw.tap != nil {
			for i, t := range b.taps[pw.tap.src] {
				if t == pw.tap.tap {
					from = circuit.PinRef{Part: splitID[pw.tap.src], Pin: "t" + strconv.Itoa(i)}
				}
			}
		}
		b.emit(from, pw.to)
	}
}

func (b *Builder) emit(from, to circuit.PinRef) {
	id := b.id("w")
	b.def.Wires[id] = &circuit.Wire{ID: id, From: from, To: to}
}
