package sim

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// Location points at one part inside one instance, for error reporting.
type Location struct {
	Path []string `json:"path"` // instance path (part IDs from the root)
	Part string   `json:"part"`
	Text string   `json:"text"` // human-readable, e.g. "CPU/alu/fa3/n4"
}

// CompileError is a design problem the simulator cannot run, with the parts
// involved so the editor can highlight them.
type CompileError struct {
	Kind      string     `json:"kind"` // "loop", "drivers", "design"
	Msg       string     `json:"msg"`
	Locations []Location `json:"locations,omitempty"`
}

func (e *CompileError) Error() string { return e.Msg }

type origin struct {
	inst *Instance
	part string
}

type prim struct {
	in  [2]int32 // slots
	out int32    // slot
	org origin
}

type srcRec struct {
	kind SourceKind
	slot int32
	pin  string
	org  origin
}

type memRec struct {
	part            *circuit.Part
	org             origin
	addr, din, dout []int32
	we, en, clk     int32
}

type driverKind uint8

const (
	drvNone driverKind = iota
	drvConst
	drvSource
	drvGate
)

type driver struct {
	kind driverKind
	idx  int32 // const value, source signal, or gate index
	org  origin
	pin  string
}

type flattener struct {
	d       *circuit.Design
	uf      []int32
	nands   []prim
	dffs    []prim // in[0]=d, in[1]=clk
	mems    []memRec
	srcs    []srcRec
	consts  []struct{ slot, val int32 }
	insts   []*Instance
	defName map[*Instance]string
}

// Compile flattens the design rooted at d.Root into a levelized netlist.
// It rejects multiply-driven nets and combinational loops with errors that
// name the parts involved.
func Compile(d *circuit.Design) (*Netlist, error) {
	if err := d.Validate(); err != nil {
		return nil, &CompileError{Kind: "design", Msg: err.Error()}
	}
	f := &flattener{d: d, defName: map[*Instance]string{}}
	root, err := f.instantiate(d.Chips[d.Root], nil)
	if err != nil {
		return nil, err
	}
	return f.build(root)
}

func (f *flattener) alloc(n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(len(f.uf))
		f.uf = append(f.uf, int32(len(f.uf)))
	}
	return out
}

func (f *flattener) find(x int32) int32 {
	for f.uf[x] != x {
		f.uf[x] = f.uf[f.uf[x]]
		x = f.uf[x]
	}
	return x
}

func (f *flattener) union(a, b []int32) {
	for i := range a {
		ra, rb := f.find(a[i]), f.find(b[i])
		if ra != rb {
			f.uf[rb] = ra
		}
	}
}

func (f *flattener) instantiate(def *circuit.ChipDef, path []string) (*Instance, error) {
	inst := &Instance{Path: path, Def: def.ID, Children: map[string]*Instance{}, pins: map[string]map[string][]int32{}}
	f.insts = append(f.insts, inst)
	f.defName[inst] = def.Name
	for _, pid := range def.SortedPartIDs() {
		p := def.Parts[pid]
		shape, err := f.d.Shape(p)
		if err != nil {
			return nil, &CompileError{Kind: "design", Msg: err.Error()}
		}
		pins := map[string][]int32{}
		for _, ps := range shape.Pins {
			pins[ps.Name] = f.alloc(ps.Width)
		}
		inst.pins[pid] = pins
		org := origin{inst, pid}
		switch p.Kind {
		case circuit.KindNand:
			f.nands = append(f.nands, prim{in: [2]int32{pins["a"][0], pins["b"][0]}, out: pins["y"][0], org: org})
		case circuit.KindDFF:
			f.dffs = append(f.dffs, prim{in: [2]int32{pins["d"][0], pins["clk"][0]}, out: pins["q"][0], org: org})
		case circuit.KindConst:
			for i, s := range pins["q"] {
				f.consts = append(f.consts, struct{ slot, val int32 }{s, int32(p.Value >> uint(i) & 1)})
			}
		case circuit.KindSwitch, circuit.KindButton, circuit.KindClock:
			kind := map[circuit.Kind]SourceKind{circuit.KindSwitch: SourceSwitch, circuit.KindButton: SourceButton, circuit.KindClock: SourceClock}[p.Kind]
			f.srcs = append(f.srcs, srcRec{kind: kind, slot: pins["q"][0], pin: "q", org: org})
		case circuit.KindJoystick:
			for _, n := range circuit.JoystickPins {
				f.srcs = append(f.srcs, srcRec{kind: SourceJoystick, slot: pins[n][0], pin: n, org: org})
			}
		case circuit.KindRAM, circuit.KindROM, circuit.KindMatrix:
			m := memRec{part: p, org: org, addr: pins["addr"], dout: pins["dout"], clk: pins["clk"][0], we: -1, en: -1}
			if p.Kind == circuit.KindROM {
				m.en = pins["en"][0]
			} else {
				m.din, m.we = pins["din"], pins["we"][0]
			}
			f.mems = append(f.mems, m)
		case circuit.KindSplitter:
			bus := pins["bus"]
			for i, t := range p.EffectiveTaps() {
				f.union(bus[t.Lo:t.Hi], pins[fmt.Sprintf("t%d", i)])
			}
		case circuit.KindChip:
			child, err := f.instantiate(f.d.Chips[p.Chip], append(append([]string(nil), path...), pid))
			if err != nil {
				return nil, err
			}
			inst.Children[pid] = child
			ins, outs := f.d.Chips[p.Chip].Interface()
			for _, ip := range ins {
				f.union(pins[ip.ID], child.pins[ip.ID]["q"])
			}
			for _, op := range outs {
				f.union(pins[op.ID], child.pins[op.ID]["a"])
			}
		}
	}
	for _, wid := range def.SortedWireIDs() {
		w := def.Wires[wid]
		f.union(inst.pins[w.From.Part][w.From.Pin], inst.pins[w.To.Part][w.To.Pin])
	}
	return inst, nil
}

func (f *flattener) loc(o origin) Location {
	names := append([]string{f.defName[f.insts[0]]}, o.inst.Path...)
	return Location{Path: o.inst.Path, Part: o.part, Text: strings.Join(append(names, o.part), "/")}
}

// build resolves nets, assigns signals, levelizes the gates and assembles the netlist.
func (f *flattener) build(root *Instance) (*Netlist, error) {
	drivers := make([]driver, len(f.uf))
	nl := &Netlist{root: root, sourceIdx: map[string]int{}, memIdx: map[string]int{}}
	next := int32(2)
	claim := func(slot int32, dv driver) error {
		r := f.find(slot)
		if prev := drivers[r]; prev.kind != drvNone {
			return &CompileError{Kind: "drivers",
				Msg:       fmt.Sprintf("a net is driven by both %s.%s and %s.%s", f.loc(prev.org).Text, prev.pin, f.loc(dv.org).Text, dv.pin),
				Locations: []Location{f.loc(prev.org), f.loc(dv.org)}}
		}
		drivers[r] = dv
		return nil
	}
	for _, c := range f.consts {
		if err := claim(c.slot, driver{kind: drvConst, idx: c.val}); err != nil {
			return nil, err
		}
	}
	newSource := func(slot int32, org origin, pin string) (int32, error) {
		s := next
		next++
		return s, claim(slot, driver{kind: drvSource, idx: s, org: org, pin: pin})
	}
	for _, s := range f.srcs {
		sig, err := newSource(s.slot, s.org, s.pin)
		if err != nil {
			return nil, err
		}
		key := s.org.inst.Key(s.org.part)
		if s.kind == SourceJoystick {
			key += "." + s.pin
		}
		nl.sourceIdx[key] = len(nl.sources)
		nl.sources = append(nl.sources, Source{Key: key, Kind: s.kind, Signal: sig, Inst: s.org.inst, Part: s.org.part, Pin: s.pin})
		if s.kind == SourceClock {
			nl.clocks = append(nl.clocks, sig)
		}
	}
	for _, d := range f.dffs {
		sig, err := newSource(d.out, d.org, "q")
		if err != nil {
			return nil, err
		}
		nl.dffQ = append(nl.dffQ, sig)
		nl.dffKey = append(nl.dffKey, d.org.inst.Key(d.org.part))
	}
	memOut := make([][]int32, len(f.mems))
	for i, m := range f.mems {
		for _, slot := range m.dout {
			sig, err := newSource(slot, m.org, "dout")
			if err != nil {
				return nil, err
			}
			memOut[i] = append(memOut[i], sig)
		}
	}
	for g, n := range f.nands {
		if err := claim(n.out, driver{kind: drvGate, idx: int32(g), org: n.org, pin: "y"}); err != nil {
			return nil, err
		}
	}
	gateBase := next
	nl.gateBase = gateBase

	// Levelize with Kahn's algorithm over gate-to-gate edges.
	nGates := len(f.nands)
	inGate := make([]int32, 2*nGates) // driving gate of each input, or -1
	indeg := make([]int32, nGates)
	users := make([][]int32, nGates)
	for g, n := range f.nands {
		for k := 0; k < 2; k++ {
			dv := drivers[f.find(n.in[k])]
			inGate[2*g+k] = -1
			if dv.kind == drvGate {
				inGate[2*g+k] = dv.idx
				indeg[g]++
				users[dv.idx] = append(users[dv.idx], int32(g))
			}
		}
	}
	level := make([]int32, nGates)
	queue := make([]int32, 0, nGates)
	for g := range nGates {
		if indeg[g] == 0 {
			queue = append(queue, int32(g))
		}
	}
	for qi := 0; qi < len(queue); qi++ {
		g := queue[qi]
		for _, u := range users[g] {
			level[u] = max(level[u], level[g]+1)
			if indeg[u]--; indeg[u] == 0 {
				queue = append(queue, u)
			}
		}
	}
	if len(queue) != nGates {
		return nil, f.loopError(indeg, inGate)
	}
	order := make([]int32, nGates)
	for i := range order {
		order[i] = int32(i)
	}
	sort.SliceStable(order, func(i, j int) bool { return level[order[i]] < level[order[j]] })
	rank := make([]int32, nGates)
	for i, g := range order {
		rank[g] = int32(i)
	}
	sigOf := func(slot int32) int32 {
		dv := drivers[f.find(slot)]
		switch dv.kind {
		case drvConst:
			return dv.idx
		case drvSource:
			return dv.idx
		case drvGate:
			return gateBase + rank[dv.idx]
		}
		return sigZero
	}
	nl.numSignals = int(gateBase) + nGates
	nl.gateIn = make([]int32, 2*nGates)
	maxLevel := int32(0)
	for i, g := range order {
		n := f.nands[g]
		nl.gateIn[2*i], nl.gateIn[2*i+1] = sigOf(n.in[0]), sigOf(n.in[1])
		maxLevel = max(maxLevel, level[g]+1)
	}
	nl.buildFanout()

	domIdx := map[int32]int{}
	dom := func(clk int32) *domain {
		i, ok := domIdx[clk]
		if !ok {
			i = len(nl.domains)
			domIdx[clk] = i
			nl.domains = append(nl.domains, domain{clk: clk})
		}
		return &nl.domains[i]
	}
	for i, d := range f.dffs {
		nl.dffD = append(nl.dffD, sigOf(d.in[0]))
		dm := dom(sigOf(d.in[1]))
		dm.dffs = append(dm.dffs, int32(i))
	}
	mapSlots := func(slots []int32) []int32 {
		out := make([]int32, len(slots))
		for i, s := range slots {
			out[i] = sigOf(s)
		}
		return out
	}
	for i, m := range f.mems {
		spec := memSpec{kind: m.part.Kind, key: m.org.inst.Key(m.org.part), inst: m.org.inst, part: m.org.part,
			addr: mapSlots(m.addr), din: mapSlots(m.din), dout: memOut[i], we: sigOne, en: sigOne, clk: sigOf(m.clk),
			init: m.part.Words}
		if m.we >= 0 {
			spec.we = sigOf(m.we)
		}
		if m.en >= 0 {
			spec.en = sigOf(m.en)
		}
		spec.size = 1 << uint(len(spec.addr))
		spec.dataMask = uint16(1<<uint(len(spec.dout)) - 1)
		nl.memIdx[spec.key] = len(nl.mems)
		nl.mems = append(nl.mems, spec)
		dm := dom(spec.clk)
		dm.mems = append(dm.mems, int32(i))
	}
	for _, inst := range f.insts {
		for _, pins := range inst.pins {
			for name, slots := range pins {
				pins[name] = mapSlots(slots)
			}
		}
	}
	nl.stats = Stats{Nands: nGates, DFFs: len(f.dffs), Memories: len(f.mems), Signals: nl.numSignals, Levels: int(maxLevel)}
	return nl, nil
}

func (nl *Netlist) buildFanout() {
	nGates := len(nl.gateIn) / 2
	count := make([]int32, nl.numSignals+1)
	for g := range nGates {
		a, b := nl.gateIn[2*g], nl.gateIn[2*g+1]
		count[a+1]++
		if b != a {
			count[b+1]++
		}
	}
	for i := 1; i < len(count); i++ {
		count[i] += count[i-1]
	}
	nl.foOff = count
	nl.foGate = make([]int32, count[nl.numSignals])
	fill := append([]int32(nil), count[:nl.numSignals]...)
	for g := range nGates {
		a, b := nl.gateIn[2*g], nl.gateIn[2*g+1]
		nl.foGate[fill[a]] = int32(g)
		fill[a]++
		if b != a {
			nl.foGate[fill[b]] = int32(g)
			fill[b]++
		}
	}
}

// loopError finds one concrete combinational cycle among the gates Kahn's
// algorithm could not order. Every such gate has at least one unordered input
// gate, so walking backwards must revisit a gate; that revisit closes a loop.
func (f *flattener) loopError(indeg, inGate []int32) error {
	start := int32(-1)
	for g, d := range indeg {
		if d > 0 {
			start = int32(g)
			break
		}
	}
	seenAt := map[int32]int{}
	var walk []int32
	g := start
	for {
		if at, ok := seenAt[g]; ok {
			walk = walk[at:]
			break
		}
		seenAt[g] = len(walk)
		walk = append(walk, g)
		for k := int32(0); k < 2; k++ {
			if h := inGate[2*g+k]; h >= 0 && indeg[h] > 0 {
				g = h
				break
			}
		}
	}
	// walk follows inputs backwards; report it in signal-flow order.
	locs := make([]Location, len(walk))
	texts := make([]string, len(walk))
	for i, gi := range walk {
		l := f.loc(f.nands[gi].org)
		locs[len(walk)-1-i] = l
		texts[len(walk)-1-i] = l.Text
	}
	return &CompileError{Kind: "loop",
		Msg:       fmt.Sprintf("combinational loop through %d gates: %s -> back to start; break it with a DFF", len(walk), strings.Join(texts, " -> ")),
		Locations: locs}
}
