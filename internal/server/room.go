package server

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/sim"
)

// Simulation pacing limits.
const (
	frameInterval = time.Second / 30
	simInterval   = 4 * time.Millisecond
	sliceBudget   = 3 * time.Millisecond
	maxHz         = 5_000_000
	defaultHz     = 60_000
	undoDepth     = 200
)

// Binary frame tags (first byte of a binary WebSocket message).
const (
	frameSignals byte = 1
	frameDock    byte = 2
)

type inbound struct {
	c   *client
	msg clientMsg
}

type clientMsg struct {
	Type   string   `json:"type"`
	Name   string   `json:"name,omitempty"`
	CID    string   `json:"cid,omitempty"`
	Op     *Op      `json:"op,omitempty"`
	X      float64  `json:"x,omitempty"`
	Y      float64  `json:"y,omitempty"`
	Path   []string `json:"path,omitempty"`
	Rect   *rect    `json:"rect,omitempty"`
	Action string   `json:"action,omitempty"`
	Hz     float64  `json:"hz,omitempty"`
	Bits   uint8    `json:"bits,omitempty"`
	Part   string   `json:"part,omitempty"`
	Down   bool     `json:"down,omitempty"`
}

type rect struct{ X0, Y0, X1, Y1 float64 }

// watch is what one client sees: the signals of the wires and pins of the
// parts inside its viewport, in the chip instance it has drilled into.
type watch struct {
	vid  uint32
	sigs []int32
	last []byte
}

// Room is one shared world. All state is owned by the room goroutine; clients
// talk to it through the inbox, so there are no locks around the design or
// the simulator.
type Room struct {
	id       string
	tmpl     string
	inbox    chan inbound
	leave    chan *client
	design   *circuit.Design
	seq      uint64
	clients  map[*client]*watch
	undo     map[string][]Op
	redo     map[string][]Op
	sim      *sim.Sim
	stats    sim.Stats
	compErr  *sim.CompileError
	running  bool
	hz       float64
	cycles   uint64
	joy      map[*client]uint8
	store    *Store
	dirty    bool
	lastDock []byte
	vid      uint32
	measure  struct {
		at     time.Time
		cycles uint64
		hz     float64
	}
}

func newRoom(id, tmpl string, d *circuit.Design, seq uint64, st *Store) *Room {
	r := &Room{id: id, tmpl: tmpl, inbox: make(chan inbound, 1024), leave: make(chan *client, 64), design: d, seq: seq,
		clients: map[*client]*watch{}, undo: map[string][]Op{}, redo: map[string][]Op{}, hz: defaultHz,
		running: tmpl != "blank", joy: map[*client]uint8{}, store: st}
	r.recompile()
	return r
}

// recompile rebuilds the netlist after an edit, carrying flip-flop and
// memory state across so editing a gate does not restart a running program.
func (r *Room) recompile() {
	nl, err := sim.Compile(r.design)
	if err != nil {
		var ce *sim.CompileError
		if !errors.As(err, &ce) {
			ce = &sim.CompileError{Kind: "design", Msg: err.Error()}
		}
		r.compErr = ce
		return
	}
	next := sim.New(nl, sim.ModeEvent)
	if r.sim != nil {
		next.Import(r.sim.Export())
	}
	r.sim, r.stats, r.compErr = next, nl.Stats(), nil
	r.applyJoystick()
	r.vid++
	for c, w := range r.clients {
		r.rebuildWatch(c, w)
	}
}

func (r *Room) run(done <-chan struct{}) {
	frames := time.NewTicker(frameInterval)
	steps := time.NewTicker(simInterval)
	save := time.NewTicker(2 * time.Second)
	defer frames.Stop()
	defer steps.Stop()
	defer save.Stop()
	last := time.Now()
	owed := 0.0
	r.measure.at = last
	for {
		select {
		case <-done:
			r.flush()
			return
		case in := <-r.inbox:
			r.handle(in.c, in.msg)
		case c := <-r.leave:
			r.drop(c)
		case now := <-steps.C:
			// Nobody watching: the clock stops, so idle rooms cost no CPU.
			if r.running && r.sim != nil && len(r.clients) > 0 {
				owed += r.hz * now.Sub(last).Seconds()
				owed = min(owed, r.hz*0.25) // never try to catch up more than 250 ms
				deadline := now.Add(sliceBudget)
				for owed >= 1 && time.Now().Before(deadline) {
					n := min(int(owed), 256)
					if !r.cycle(n) {
						break
					}
					owed -= float64(n)
				}
			}
			last = now
		case now := <-frames.C:
			r.stream(now)
		case <-save.C:
			r.flush()
		}
	}
}

func (r *Room) cycle(n int) bool {
	for range n {
		if err := r.sim.Cycle(); err != nil {
			r.running = false
			r.compErr = &sim.CompileError{Kind: "runtime", Msg: err.Error()}
			r.broadcastSim()
			return false
		}
		r.cycles++
	}
	return true
}

func (r *Room) flush() {
	if !r.dirty || r.store == nil {
		return
	}
	if err := r.store.Save(r.id, r.tmpl, r.design, r.seq); err != nil {
		slog.Error("save room", "room", r.id, "err", err)
		return
	}
	r.dirty = false
}

type peerInfo struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Color string   `json:"color"`
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	Path  []string `json:"path"`
}

func (r *Room) peers() []peerInfo {
	out := []peerInfo{}
	for c := range r.clients {
		path := c.path
		if path == nil {
			path = []string{}
		}
		out = append(out, peerInfo{ID: c.id, Name: c.name, Color: c.color, X: c.x, Y: c.y, Path: path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Room) simState() map[string]any {
	return map[string]any{"type": "sim", "running": r.running, "hz": r.hz, "actualHz": r.measure.hz,
		"cycles": r.cycles, "error": r.compErr, "stats": r.stats}
}

func (r *Room) broadcast(v any) {
	b, _ := json.Marshal(v)
	for c := range r.clients {
		c.sendJSON(b)
	}
}

func (r *Room) broadcastSim() { r.broadcast(r.simState()) }

func (r *Room) handle(c *client, m clientMsg) {
	switch m.Type {
	case "hello":
		if m.Name != "" && len(m.Name) <= 24 {
			c.name = m.Name
		}
		r.clients[c] = &watch{}
		r.lastDock = nil // the newcomer needs the current screen
		c.sendValue(map[string]any{"type": "welcome", "you": peerInfo{ID: c.id, Name: c.name, Color: c.color},
			"doc": r.design, "seq": r.seq, "templates": Templates, "template": r.tmpl})
		c.sendValue(r.simState())
		r.broadcast(map[string]any{"type": "peers", "peers": r.peers()})
	case "op":
		if m.Op == nil {
			return
		}
		r.edit(c, *m.Op, m.CID, true)
	case "undo", "redo":
		from, to := r.undo, r.redo
		if m.Type == "redo" {
			from, to = r.redo, r.undo
		}
		stack := from[c.id]
		if len(stack) == 0 {
			return
		}
		op := stack[len(stack)-1]
		from[c.id] = stack[:len(stack)-1]
		inv, ok := r.commit(c, op, "")
		if ok {
			to[c.id] = append(to[c.id], inv)
		}
	case "name":
		if n := strings.TrimSpace(m.Name); n != "" && len(n) <= 24 {
			c.name = n
			r.broadcast(map[string]any{"type": "peers", "peers": r.peers()})
		}
	case "cursor":
		c.x, c.y, c.path = m.X, m.Y, m.Path
		r.broadcast(map[string]any{"type": "cursor", "id": c.id, "x": m.X, "y": m.Y, "path": m.Path})
	case "view":
		if w, ok := r.clients[c]; ok && m.Rect != nil {
			c.path, c.view = m.Path, *m.Rect
			r.rebuildWatch(c, w)
		}
	case "sim":
		r.control(m)
	case "joy":
		r.joy[c] = m.Bits & 0x1F
		r.applyJoystick()
	case "press":
		r.press(m.Path, m.Part, m.Down)
	}
}

func (r *Room) edit(c *client, op Op, cid string, record bool) {
	inv, ok := r.commit(c, op, cid)
	if ok && record {
		r.undo[c.id] = append(r.undo[c.id], inv)
		if len(r.undo[c.id]) > undoDepth {
			r.undo[c.id] = r.undo[c.id][1:]
		}
		delete(r.redo, c.id)
	}
}

// commit applies an op, sequences it and broadcasts it (or rejects it).
func (r *Room) commit(c *client, op Op, cid string) (Op, bool) {
	inv, err := Apply(r.design, op)
	if err != nil {
		c.sendValue(map[string]any{"type": "reject", "cid": cid, "error": err.Error()})
		return Op{}, false
	}
	r.seq++
	r.dirty = true
	r.broadcast(map[string]any{"type": "op", "seq": r.seq, "author": c.id, "cid": cid, "op": op})
	r.recompile()
	r.broadcastSim()
	return inv, true
}

func (r *Room) control(m clientMsg) {
	switch m.Action {
	case "run":
		r.running = r.sim != nil
	case "pause":
		r.running = false
	case "step":
		r.running = false
		if r.sim != nil {
			r.cycle(1)
		}
	case "speed":
		if m.Hz >= 1 && m.Hz <= maxHz {
			r.hz = m.Hz
		}
	}
	r.broadcastSim()
}

func (r *Room) applyJoystick() {
	if r.sim == nil {
		return
	}
	var bits uint8
	for _, b := range r.joy {
		bits |= b
	}
	for _, src := range r.sim.Netlist().Sources() {
		if src.Kind != sim.SourceJoystick {
			continue
		}
		for i, pin := range circuit.JoystickPins {
			if pin == src.Pin {
				_ = r.sim.Poke(src.Key, bits>>uint(i)&1)
			}
		}
	}
	_ = r.sim.Propagate()
}

func (r *Room) press(path []string, part string, down bool) {
	if r.sim == nil {
		return
	}
	inst := r.sim.Netlist().Root().Find(path)
	if inst == nil {
		return
	}
	key := inst.Key(part)
	for _, src := range r.sim.Netlist().Sources() {
		if src.Key != key {
			continue
		}
		v := uint8(0)
		if src.Kind == sim.SourceSwitch {
			v = 1 ^ r.sim.Value(src.Signal) // switches toggle on press
			if !down {
				return
			}
		} else if down {
			v = 1
		}
		_ = r.sim.Poke(key, v)
		_ = r.sim.Propagate()
	}
}

func (r *Room) drop(c *client) {
	if _, ok := r.clients[c]; !ok {
		return
	}
	delete(r.clients, c)
	delete(r.joy, c)
	r.applyJoystick()
	r.broadcast(map[string]any{"type": "peers", "peers": r.peers()})
}

// rebuildWatch recomputes which signals a client sees: every wire and every
// pin of every part that intersects its viewport in its current instance.
func (r *Room) rebuildWatch(c *client, w *watch) {
	r.vid++
	w.vid, w.sigs, w.last = r.vid, nil, nil
	type wireRef struct {
		ID    string `json:"id"`
		Width int    `json:"w"`
	}
	type pinRef struct {
		Part  string `json:"part"`
		Pin   string `json:"pin"`
		Width int    `json:"w"`
	}
	msg := struct {
		Type  string    `json:"type"`
		VID   uint32    `json:"vid"`
		Wires []wireRef `json:"wires"`
		Pins  []pinRef  `json:"pins"`
	}{Type: "watch", VID: w.vid, Wires: []wireRef{}, Pins: []pinRef{}}
	if r.sim == nil {
		c.sendValue(msg)
		return
	}
	inst := r.sim.Netlist().Root().Find(c.path)
	if inst == nil {
		c.sendValue(msg)
		return
	}
	def := r.design.Chips[inst.Def]
	visible := map[string]bool{}
	for _, id := range def.SortedPartIDs() {
		p := def.Parts[id]
		s, err := r.design.Shape(p)
		if err != nil {
			continue
		}
		const margin = 4
		if float64(p.X+s.W+margin) < c.view.X0 || float64(p.X-margin) > c.view.X1 ||
			float64(p.Y+s.H+margin) < c.view.Y0 || float64(p.Y-margin) > c.view.Y1 {
			continue
		}
		visible[id] = true
		for _, pin := range s.Pins {
			sigs := inst.Pin(id, pin.Name)
			msg.Pins = append(msg.Pins, pinRef{id, pin.Name, len(sigs)})
			w.sigs = append(w.sigs, sigs...)
		}
	}
	for _, id := range def.SortedWireIDs() {
		wire := def.Wires[id]
		if !visible[wire.From.Part] && !visible[wire.To.Part] && !routeVisible(wire, c.view) {
			continue
		}
		sigs := inst.Pin(wire.From.Part, wire.From.Pin)
		msg.Wires = append(msg.Wires, wireRef{id, len(sigs)})
		w.sigs = append(w.sigs, sigs...)
	}
	c.sendValue(msg)
}

func routeVisible(w *circuit.Wire, v rect) bool {
	for _, p := range w.Route {
		if float64(p.X) >= v.X0 && float64(p.X) <= v.X1 && float64(p.Y) >= v.Y0 && float64(p.Y) <= v.Y1 {
			return true
		}
	}
	return false
}

// stream sends each client the signals it watches (only when they changed)
// and the dock (screen, score, LEDs) when the display changed.
func (r *Room) stream(now time.Time) {
	if dt := now.Sub(r.measure.at); dt >= time.Second {
		r.measure.hz = float64(r.cycles-r.measure.cycles) / dt.Seconds()
		r.measure.at, r.measure.cycles = now, r.cycles
		r.broadcastSim()
	}
	if r.sim == nil {
		return
	}
	for c, w := range r.clients {
		n := len(w.sigs)
		frame := make([]byte, 5+(n+7)/8)
		frame[0] = frameSignals
		binary.LittleEndian.PutUint32(frame[1:5], w.vid)
		for i, s := range w.sigs {
			if r.sim.Value(s) == 1 {
				frame[5+i/8] |= 1 << uint(i%8)
			}
		}
		if string(frame) != string(w.last) {
			w.last = frame
			c.sendFrame(frameSignals, frame)
		}
	}
	if dock := r.dock(); dock != nil {
		for c := range r.clients {
			c.sendFrame(frameDock, dock)
		}
	}
}

// dock encodes the root-level displays: [tag][256 pixels][seg7 bytes...][LED
// byte][PC][IR hi][IR lo], sent only when something visible changed.
func (r *Room) dock() []byte {
	root := r.design.Chips[r.design.Root]
	out := []byte{frameDock}
	var matrix []uint16
	inst := r.sim.Netlist().Root()
	var segs []byte
	var leds byte
	nLED := 0
	var rom string
	for _, id := range root.SortedPartIDs() {
		p := root.Parts[id]
		switch p.Kind {
		case circuit.KindMatrix:
			if matrix == nil {
				matrix, _, _ = r.sim.Memory(id)
			}
		case circuit.KindSeg7:
			var v byte
			for i, n := range circuit.SegmentNames {
				v |= byte(r.sim.Bits(inst.Pin(id, n))) << uint(i)
			}
			segs = append(segs, v)
		case circuit.KindLED:
			if nLED < 8 {
				leds |= byte(r.sim.Bits(inst.Pin(id, "a"))) << uint(nLED)
				nLED++
			}
		case circuit.KindROM:
			if rom == "" && p.DataBits == 16 {
				rom = id
			}
		}
	}
	for i := range 256 {
		if i < len(matrix) {
			out = append(out, byte(matrix[i]))
		} else {
			out = append(out, 0)
		}
	}
	out = append(out, byte(len(segs)))
	out = append(out, segs...)
	out = append(out, leds)
	if rom != "" {
		ir := uint16(r.sim.Bits(inst.Pin(rom, "dout")))
		out = append(out, byte(r.sim.Bits(inst.Pin(rom, "addr"))), byte(ir>>8), byte(ir))
	}
	if string(out) == string(r.lastDock) {
		return nil
	}
	r.lastDock = out
	return out
}
