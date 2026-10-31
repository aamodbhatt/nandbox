package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/hdl"
	"github.com/coder/websocket"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpsInvertExactly(t *testing.T) {
	d, err := NewTemplate("blank")
	if err != nil {
		t.Fatal(err)
	}
	w := hdl.WorldID
	ops := []Op{
		{T: "addPart", Chip: w, Part: &circuit.Part{ID: "n1", Kind: circuit.KindNand, X: 4, Y: 4}},
		{T: "addPart", Chip: w, Part: &circuit.Part{ID: "s1", Kind: circuit.KindSwitch}},
		{T: "addWire", Chip: w, Wire: &circuit.Wire{ID: "w1", From: circuit.PinRef{Part: "s1", Pin: "q"}, To: circuit.PinRef{Part: "n1", Pin: "a"}}},
		{T: "movePart", Chip: w, ID: "n1", X: 10, Y: 2},
		{T: "setLabel", Chip: w, ID: "n1", Label: "gate"},
		{T: "addPart", Chip: w, Part: &circuit.Part{ID: "fa", Kind: circuit.KindChip, Chip: "FullAdder"}},
		{T: "removePart", Chip: w, ID: "s1"},
		{T: "batch", Ops: []Op{{T: "removePart", Chip: w, ID: "n1"}, {T: "removePart", Chip: w, ID: "fa"}}},
		{T: "addChip", Def: &circuit.ChipDef{ID: "mine", Name: "Mine", Parts: map[string]*circuit.Part{
			"i": {ID: "i", Kind: circuit.KindIn, Width: 1}, "o": {ID: "o", Kind: circuit.KindOut, Width: 1, Y: 2}, "g": {ID: "g", Kind: circuit.KindNand},
		}, Wires: map[string]*circuit.Wire{
			"a": {ID: "a", From: circuit.PinRef{Part: "i", Pin: "q"}, To: circuit.PinRef{Part: "g", Pin: "a"}},
			"b": {ID: "b", From: circuit.PinRef{Part: "g", Pin: "y"}, To: circuit.PinRef{Part: "o", Pin: "a"}},
		}}},
		{T: "addPart", Chip: w, Part: &circuit.Part{ID: "m1", Kind: circuit.KindChip, Chip: "mine"}},
	}
	for i, op := range ops {
		before := mustJSON(t, d)
		inv, err := Apply(d, op)
		if err != nil {
			t.Fatalf("op %d (%s): %v", i, op.T, err)
		}
		if err := d.Validate(); err != nil {
			t.Fatalf("op %d left an invalid design: %v", i, err)
		}
		after := mustJSON(t, d)
		if _, err := Apply(d, inv); err != nil {
			t.Fatalf("inverse of op %d: %v", i, err)
		}
		if got := mustJSON(t, d); got != before {
			t.Fatalf("inverse of op %d (%s) did not restore the design", i, op.T)
		}
		if _, err := Apply(d, op); err != nil { // redo
			t.Fatal(err)
		}
		if mustJSON(t, d) != after {
			t.Fatalf("re-applying op %d diverged", i)
		}
	}
}

func TestInvalidOpsAreRejectedAtomically(t *testing.T) {
	d, _ := NewTemplate("blank")
	w := hdl.WorldID
	before := mustJSON(t, d)
	bad := []Op{
		{T: "addPart", Chip: w, Part: &circuit.Part{ID: "x", Kind: "warp-drive"}},
		{T: "addWire", Chip: w, Wire: &circuit.Wire{ID: "w", From: circuit.PinRef{Part: "nope", Pin: "q"}, To: circuit.PinRef{Part: "nope2", Pin: "a"}}},
		{T: "addPart", Chip: "Not", Part: &circuit.Part{ID: "loop", Kind: circuit.KindChip, Chip: "Not"}},
		{T: "removeChip", ID: "FullAdder"},
		{T: "addChip", Def: &circuit.ChipDef{ID: "bad", Name: "Bad", Parts: map[string]*circuit.Part{"x": {ID: "x", Kind: circuit.KindChip, Chip: "bad"}}}},
		{T: "batch", Ops: []Op{
			{T: "addPart", Chip: w, Part: &circuit.Part{ID: "ok", Kind: circuit.KindNand}},
			{T: "removePart", Chip: w, ID: "missing"},
		}},
	}
	for i, op := range bad {
		if _, err := Apply(d, op); err == nil {
			t.Fatalf("bad op %d was accepted", i)
		}
		if mustJSON(t, d) != before {
			t.Fatalf("bad op %d changed the design", i)
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "rooms.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d, _ := NewTemplate("fib")
	if err := st.Save("abc123", "fib", d, 7); err != nil {
		t.Fatal(err)
	}
	tmpl, got, seq, ok, err := st.Load("abc123")
	if err != nil || !ok || tmpl != "fib" || seq != 7 || mustJSON(t, got) != mustJSON(t, d) {
		t.Fatalf("round trip failed: ok=%v err=%v tmpl=%s seq=%d", ok, err, tmpl, seq)
	}
	if _, _, _, ok, _ := st.Load("missing"); ok {
		t.Fatal("missing room reported as found")
	}
}

func TestSlowClientIsDroppedNotBlocking(t *testing.T) {
	c := newClient("abcdef", 0, nil)
	for range sendQueue {
		c.sendJSON([]byte("{}"))
	}
	select {
	case <-c.closed:
		t.Fatal("closed before the queue was full")
	default:
	}
	c.sendJSON([]byte("{}")) // one too many: must not block, must disconnect
	select {
	case <-c.closed:
	default:
		t.Fatal("overflowing client was not closed")
	}
	// Frames coalesce: only the newest per tag is kept.
	c.sendFrame(frameSignals, []byte{1, 1})
	c.sendFrame(frameSignals, []byte{1, 2})
	if fs := c.takeFrames(); len(fs) != 1 || fs[0][1] != 2 {
		t.Fatalf("frames did not coalesce: %v", fs)
	}
}

// peer is a test WebSocket client.
type peer struct {
	t    *testing.T
	conn *websocket.Conn
	ctx  context.Context
}

func dial(t *testing.T, ctx context.Context, url string) *peer {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(16 << 20)
	return &peer{t, conn, ctx}
}

func (p *peer) send(v any) {
	p.t.Helper()
	if err := p.conn.Write(p.ctx, websocket.MessageText, []byte(mustJSON(p.t, v))); err != nil {
		p.t.Fatal(err)
	}
}

// next returns the next JSON message of the given type (skipping others).
func (p *peer) next(typ string) map[string]any {
	p.t.Helper()
	for {
		kind, data, err := p.conn.Read(p.ctx)
		if err != nil {
			p.t.Fatalf("waiting for %q: %v", typ, err)
		}
		if kind != websocket.MessageText {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			p.t.Fatal(err)
		}
		if m["type"] == typ {
			return m
		}
	}
}

func (p *peer) nextBinary(tag byte) []byte {
	p.t.Helper()
	for {
		kind, data, err := p.conn.Read(p.ctx)
		if err != nil {
			p.t.Fatalf("waiting for frame %d: %v", tag, err)
		}
		if kind == websocket.MessageBinary && data[0] == tag {
			return data
		}
	}
}

func TestRoomProtocol(t *testing.T) {
	srv := New(nil, fstest.MapFS{"index.html": {Data: []byte("<html>")}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	res, err := http.Post(ts.URL+"/api/rooms", "application/json", strings.NewReader(`{"template":"blank"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ ID string }
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	if created.ID == "" {
		t.Fatal("no room id")
	}
	if r, _ := http.Get(ts.URL + "/r/" + created.ID); r.StatusCode != http.StatusOK {
		t.Fatalf("share link status %d", r.StatusCode)
	}
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/rooms/" + created.ID + "/ws"
	a, b := dial(t, ctx, url), dial(t, ctx, url)
	a.send(map[string]any{"type": "hello", "name": "ada"})
	welcome := a.next("welcome")
	if welcome["doc"] == nil {
		t.Fatal("welcome without a document")
	}
	b.send(map[string]any{"type": "hello", "name": "bob"})
	b.next("welcome")
	if peers := a.next("peers")["peers"].([]any); len(peers) < 1 {
		t.Fatal("no presence list")
	}

	// A sequenced edit reaches both peers with the same sequence number.
	add := Op{T: "addPart", Chip: hdl.WorldID, Part: &circuit.Part{ID: "g1", Kind: circuit.KindNand, X: 2, Y: 2}}
	a.send(map[string]any{"type": "op", "cid": "a-1", "op": add})
	ma, mb := a.next("op"), b.next("op")
	if ma["seq"] != mb["seq"] || ma["cid"] != "a-1" {
		t.Fatalf("sequencing mismatch: %v vs %v", ma, mb)
	}
	// Conflicting edit from B is rejected to B only.
	b.send(map[string]any{"type": "op", "cid": "b-1", "op": add})
	if rej := b.next("reject"); rej["cid"] != "b-1" {
		t.Fatalf("unexpected reject %v", rej)
	}
	// Undo is per user: A's undo removes A's gate, and everyone sees it.
	a.send(map[string]any{"type": "undo"})
	if op := b.next("op")["op"].(map[string]any); op["t"] != "removePart" {
		t.Fatalf("undo broadcast %v", op)
	}
	a.send(map[string]any{"type": "redo"})
	if op := mustJSON(t, b.next("op")["op"]); !strings.Contains(op, `"t":"addPart"`) {
		t.Fatalf("redo broadcast %v", op)
	}
	// Cursors are relayed.
	b.send(map[string]any{"type": "cursor", "x": 3.5, "y": 1, "path": []string{}})
	if cur := a.next("cursor"); cur["x"] != 3.5 {
		t.Fatalf("cursor %v", cur)
	}
	// Viewport subscription: the watch list names the gate's pins, and a
	// signal frame follows.
	a.send(map[string]any{"type": "view", "path": []string{}, "rect": map[string]float64{"X0": -10, "Y0": -10, "X1": 20, "Y1": 20}})
	watch := a.next("watch")
	if pins := watch["pins"].([]any); len(pins) != 3 {
		t.Fatalf("expected the NAND's 3 pins in view, got %v", pins)
	}
	frame := a.nextBinary(frameSignals)
	if len(frame) != 6 || frame[5] != 0b100 { // a=0, b=0, y=1
		t.Fatalf("signal frame %v", frame)
	}
	// Simulation control.
	a.send(map[string]any{"type": "sim", "action": "speed", "hz": 1000})
	for i := 0; b.next("sim")["hz"] != float64(1000); i++ {
		if i > 20 {
			t.Fatal("speed change never broadcast")
		}
	}
}

func TestSnakeRoomStreamsTheScreen(t *testing.T) {
	srv := New(nil, fstest.MapFS{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := http.Post(ts.URL+"/api/rooms", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ ID string }
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	p := dial(t, ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/rooms/"+created.ID+"/ws")
	p.send(map[string]any{"type": "hello"})
	for range 50 {
		dock := p.nextBinary(frameDock)
		lit := 0
		for _, px := range dock[1:257] {
			if px != 0 {
				lit++
			}
		}
		if lit >= 4 { // three snake cells and the food
			return
		}
	}
	t.Fatal("the snake never appeared on the streamed screen")
}

func TestRoomsSurviveRestart(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "rooms.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	first := New(st, fstest.MapFS{})
	ts := httptest.NewServer(first.Handler())
	res, err := http.Post(ts.URL+"/api/rooms", "application/json", strings.NewReader(`{"template":"blank"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ ID string }
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	p := dial(t, ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/rooms/"+created.ID+"/ws")
	p.send(map[string]any{"type": "hello"})
	p.next("welcome")
	p.send(map[string]any{"type": "op", "cid": "c1", "op": Op{T: "addPart", Chip: hdl.WorldID,
		Part: &circuit.Part{ID: "kept", Kind: circuit.KindLED, X: 1, Y: 1}}})
	p.next("op")
	_ = p.conn.Close(websocket.StatusNormalClosure, "")
	ts.Close()
	first.Shutdown() // flushes the dirty room

	second := New(st, fstest.MapFS{})
	ts2 := httptest.NewServer(second.Handler())
	defer ts2.Close()
	defer second.Shutdown()
	q := dial(t, ctx, "ws"+strings.TrimPrefix(ts2.URL, "http")+"/api/rooms/"+created.ID+"/ws")
	q.send(map[string]any{"type": "hello"})
	w := q.next("welcome")
	if w["seq"] != float64(1) || !strings.Contains(mustJSON(t, w["doc"]), `"kept"`) {
		t.Fatalf("room did not survive a restart: seq=%v", w["seq"])
	}
}
