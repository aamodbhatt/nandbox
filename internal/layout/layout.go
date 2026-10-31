// Package layout places the parts of a chip definition and routes its wires
// orthogonally, so machine-built chips read like hand-drawn schematics.
//
// It is a compact Sugiyama-style layered layout:
//  1. layers by longest path after removing depth-first back edges (the
//     feedback paths of registers and state machines);
//  2. long edges get one shared "dummy" slot per source pin and layer, so
//     wires never cut through parts;
//  3. barycenter sweeps order each layer to reduce crossings;
//  4. y positions align parts with the pins that feed them;
//  5. every channel between layers gets left-edge track assignment, and
//     feedback wires return along dedicated lanes under the drawing.
package layout

import (
	"fmt"
	"sort"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

type node struct {
	id     string
	part   *circuit.Part // nil for dummies
	shape  circuit.Shape
	layer  int
	order  float64
	y      int
	dummy  bool
	inPins map[string]int // pin name -> y offset
}

func (n *node) h() int {
	if n.dummy {
		return 0
	}
	return n.shape.H
}

type edge struct {
	wire     *circuit.Wire
	from, to *node
	fromY    int // pin offsets
	toY      int
	fromX    int
	weak     bool
	src      string // source pin key, shared by a net's wires
	chain    []*node
	feedback bool
}

// Place assigns positions to every part of c and routes every wire.
func Place(d *circuit.Design, c *circuit.ChipDef) error {
	nodes := map[string]*node{}
	var ids = c.SortedPartIDs()
	for _, id := range ids {
		p := c.Parts[id]
		s, err := d.Shape(p)
		if err != nil {
			return fmt.Errorf("layout %s: %w", c.Name, err)
		}
		n := &node{id: id, part: p, shape: s, inPins: map[string]int{}}
		for _, pin := range s.Pins {
			n.inPins[pin.Name] = pin.Y
		}
		nodes[id] = n
	}
	var edges []*edge
	succ := map[*node][]*edge{}
	pred := map[*node][]*edge{}
	for _, wid := range c.SortedWireIDs() {
		w := c.Wires[wid]
		e := &edge{wire: w, from: nodes[w.From.Part], to: nodes[w.To.Part]}
		fp, _ := e.from.shape.Pin(w.From.Pin)
		e.fromY, e.fromX = fp.Y, fp.X
		e.toY = e.to.inPins[w.To.Pin]
		e.src = w.From.String()
		edges = append(edges, e)
		succ[e.from] = append(succ[e.from], e)
		pred[e.to] = append(pred[e.to], e)
	}
	ins, outs := c.Interface()
	markFeedback(ids, nodes, succ, pred, ins)
	assignLayers(ids, nodes, succ, pred, ins, outs)
	layers := buildLayers(ids, nodes, edges, ins, outs)
	orderLayers(layers, edges)
	assignY(layers, pred)
	routeAndPlace(c, layers, edges)
	return nil
}

// markFeedback finds a small set of edges whose removal leaves a DAG: the back
// edges of a depth-first search started from the chip inputs, in pin order.
// In a valid design every cycle passes through a flip-flop or memory, so
// these are the register feedback paths, drawn as return lanes.
func markFeedback(ids []string, nodes map[string]*node, succ, pred map[*node][]*edge, ins []*circuit.Part) {
	const (
		unseen = iota
		active
		done
	)
	state := map[*node]int{}
	var visit func(n *node)
	visit = func(n *node) {
		state[n] = active
		for _, e := range succ[n] {
			switch state[e.to] {
			case active:
				e.weak = true
			case unseen:
				visit(e.to)
			}
		}
		state[n] = done
	}
	for _, p := range ins {
		if state[nodes[p.ID]] == unseen {
			visit(nodes[p.ID])
		}
	}
	for _, pass := range []bool{true, false} {
		for _, id := range ids {
			if n := nodes[id]; state[n] == unseen && (!pass || len(pred[n]) == 0) {
				visit(n)
			}
		}
	}
}

func assignLayers(ids []string, nodes map[string]*node, succ, pred map[*node][]*edge, ins, outs []*circuit.Part) {
	indeg := map[*node]int{}
	for _, id := range ids {
		n := nodes[id]
		for _, e := range pred[n] {
			if !e.weak {
				indeg[n]++
			}
		}
	}
	var queue []*node
	for _, id := range ids {
		if indeg[nodes[id]] == 0 {
			queue = append(queue, nodes[id])
		}
	}
	done := map[*node]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		done[n] = true
		for _, e := range succ[n] {
			if e.weak {
				continue
			}
			e.to.layer = max(e.to.layer, n.layer+1)
			if indeg[e.to]--; indeg[e.to] == 0 {
				queue = append(queue, e.to)
			}
		}
	}
	maxL := 0
	for _, id := range ids {
		maxL = max(maxL, nodes[id].layer)
	}
	// Anything left is on a cycle the sequential hint did not break; park it last.
	for _, id := range ids {
		if !done[nodes[id]] {
			nodes[id].layer = maxL + 1
		}
	}
	// Pure sources (constants, inputs devices) sit just before their first consumer.
	for _, id := range ids {
		n := nodes[id]
		if n.part.Kind == circuit.KindIn || len(pred[n]) > 0 || len(succ[n]) == 0 {
			continue
		}
		first := 1 << 30
		for _, e := range succ[n] {
			first = min(first, e.to.layer)
		}
		n.layer = max(0, first-1)
	}
	// Layer 0 holds only the interface inputs, which keeps that column clean.
	isIn := map[*node]bool{}
	for _, p := range ins {
		isIn[nodes[p.ID]] = true
	}
	for _, id := range ids {
		if n := nodes[id]; isIn[n] {
			n.layer = 0
		} else {
			n.layer++
		}
	}
	last := 0
	for _, id := range ids {
		if nodes[id].part.Kind != circuit.KindOut {
			last = max(last, nodes[id].layer)
		}
	}
	for _, p := range outs {
		nodes[p.ID].layer = last + 1
	}
}

func buildLayers(ids []string, nodes map[string]*node, edges []*edge, ins, outs []*circuit.Part) [][]*node {
	maxL := 0
	for _, id := range ids {
		maxL = max(maxL, nodes[id].layer)
	}
	layers := make([][]*node, maxL+1)
	for i, p := range ins {
		nodes[p.ID].order = float64(i)
	}
	for i, p := range outs {
		nodes[p.ID].order = float64(i)
	}
	for _, id := range ids {
		n := nodes[id]
		layers[n.layer] = append(layers[n.layer], n)
	}
	dummies := map[string]*node{}
	for _, e := range edges {
		if e.to.layer <= e.from.layer {
			e.feedback = true
			continue
		}
		for l := e.from.layer + 1; l < e.to.layer; l++ {
			key := fmt.Sprintf("%s@%d", e.src, l)
			dn, ok := dummies[key]
			if !ok {
				dn = &node{id: key, dummy: true, layer: l, inPins: map[string]int{}}
				dummies[key] = dn
				layers[l] = append(layers[l], dn)
			}
			e.chain = append(e.chain, dn)
		}
	}
	for l := range layers {
		sort.SliceStable(layers[l], func(i, j int) bool { return layers[l][i].order < layers[l][j].order })
		for i, n := range layers[l] {
			n.order = float64(i)
		}
	}
	return layers
}

// hops expands an edge into consecutive node pairs through its dummy chain.
func hops(e *edge) [][2]*node {
	path := append(append([]*node{e.from}, e.chain...), e.to)
	out := make([][2]*node, 0, len(path)-1)
	for i := 0; i+1 < len(path); i++ {
		out = append(out, [2]*node{path[i], path[i+1]})
	}
	return out
}

func orderLayers(layers [][]*node, edges []*edge) {
	nbr := map[*node][]*node{}
	for _, e := range edges {
		if e.feedback {
			continue
		}
		for _, h := range hops(e) {
			nbr[h[1]] = append(nbr[h[1]], h[0])
			nbr[h[0]] = append(nbr[h[0]], h[1])
		}
	}
	fixed := func(l int) bool { return l == 0 || l == len(layers)-1 }
	for sweep := range 8 {
		down := sweep%2 == 0
		for k := 1; k < len(layers); k++ {
			l := k
			if !down {
				l = len(layers) - 1 - k
			}
			if fixed(l) {
				continue
			}
			ref := l - 1
			if !down {
				ref = l + 1
			}
			for _, n := range layers[l] {
				sum, cnt := 0.0, 0
				for _, m := range nbr[n] {
					if m.layer == ref {
						sum += m.order
						cnt++
					}
				}
				if cnt > 0 {
					n.order = sum / float64(cnt)
				}
			}
			sort.SliceStable(layers[l], func(i, j int) bool { return layers[l][i].order < layers[l][j].order })
			for i, n := range layers[l] {
				n.order = float64(i)
			}
		}
	}
}

func assignY(layers [][]*node, pred map[*node][]*edge) {
	// Incoming connections per node, including dummy hops: (source y, target offset).
	type link struct {
		src  *node
		srcY int
		dstY int
	}
	links := map[*node][]link{}
	for _, es := range pred {
		for _, e := range es {
			if e.feedback {
				continue
			}
			hs := hops(e)
			for i, h := range hs {
				sy, dy := 0, 0
				if i == 0 {
					sy = e.fromY
				}
				if i == len(hs)-1 {
					dy = e.toY
				}
				links[h[1]] = append(links[h[1]], link{h[0], sy, dy})
			}
		}
	}
	for l, layer := range layers {
		bottom := -1 << 30
		for _, n := range layer {
			want := bottom
			if l > 0 && len(links[n]) > 0 {
				sum := 0
				for _, lk := range links[n] {
					sum += lk.src.y + lk.srcY - lk.dstY
				}
				want = sum / len(links[n])
			}
			gap := 2
			if n.dummy {
				gap = 1
			}
			if bottom == -1<<30 {
				n.y = max(want, 0)
				if l == 0 {
					n.y = 0
				}
			} else {
				n.y = max(want, bottom+gap)
			}
			bottom = n.y + n.h()
			if n.dummy {
				bottom = n.y
			}
		}
	}
}

type trunk struct {
	lo, hi int
	track  int
}

func routeAndPlace(c *circuit.ChipDef, layers [][]*node, edges []*edge) {
	colW := make([]int, len(layers))
	bottom := 0
	for l, layer := range layers {
		for _, n := range layer {
			if !n.dummy {
				colW[l] = max(colW[l], n.shape.W)
				bottom = max(bottom, n.y+n.h())
			}
		}
	}
	// Feedback lanes run under the drawing, one per source net.
	laneOf := map[string]int{}
	for _, e := range edges {
		if e.feedback {
			if _, ok := laneOf[e.src]; !ok {
				laneOf[e.src] = bottom + 2 + len(laneOf)
			}
		}
	}
	// Collect trunks: per channel, per net, the vertical span it needs.
	chans := make([]map[string]*trunk, len(layers))
	for i := range chans {
		chans[i] = map[string]*trunk{}
	}
	addSpan := func(ch int, key string, a, b int) {
		t, ok := chans[ch][key]
		if !ok {
			t = &trunk{lo: min(a, b), hi: max(a, b)}
			chans[ch][key] = t
		}
		t.lo, t.hi = min(t.lo, a, b), max(t.hi, a, b)
	}
	absY := func(n *node, off int) int { return n.y + off }
	for _, e := range edges {
		if e.feedback {
			lane := laneOf[e.src]
			addSpan(e.from.layer, e.src+"^", absY(e.from, e.fromY), lane)
			addSpan(e.to.layer-1, e.src+"v"+e.to.id, lane, absY(e.to, e.toY))
			continue
		}
		hs := hops(e)
		for i, h := range hs {
			sy, dy := absY(h[0], 0), absY(h[1], 0)
			if i == 0 {
				sy = absY(h[0], e.fromY)
			}
			if i == len(hs)-1 {
				dy = absY(h[1], e.toY)
			}
			addSpan(h[0].layer, fmt.Sprintf("%s#%d", e.src, h[0].layer), sy, dy)
		}
	}
	tracks := make([]int, len(layers))
	for ch, m := range chans {
		keys := make([]string, 0, len(m))
		for k, t := range m {
			if t.lo != t.hi {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := m[keys[i]], m[keys[j]]
			if a.lo != b.lo {
				return a.lo < b.lo
			}
			return keys[i] < keys[j]
		})
		var ends []int
		for _, k := range keys {
			t := m[k]
			t.track = -1
			for i, end := range ends {
				if end < t.lo {
					t.track, ends[i] = i, t.hi
					break
				}
			}
			if t.track < 0 {
				t.track = len(ends)
				ends = append(ends, t.hi)
			}
		}
		tracks[ch] = len(ends)
	}
	colX := make([]int, len(layers))
	for l := 1; l < len(layers); l++ {
		colX[l] = colX[l-1] + colW[l-1] + tracks[l-1] + 3
	}
	trackX := func(ch int, key string) int { return colX[ch] + colW[ch] + 2 + chans[ch][key].track }
	for _, layer := range layers {
		for _, n := range layer {
			if !n.dummy {
				n.part.X, n.part.Y = colX[n.layer], n.y
			}
		}
	}
	for _, e := range edges {
		var pts []circuit.Point
		if e.feedback {
			lane := laneOf[e.src]
			x1, x2 := trackX(e.from.layer, e.src+"^"), trackX(e.to.layer-1, e.src+"v"+e.to.id)
			pts = []circuit.Point{{X: x1, Y: absY(e.from, e.fromY)}, {X: x1, Y: lane}, {X: x2, Y: lane}, {X: x2, Y: absY(e.to, e.toY)}}
		} else {
			hs := hops(e)
			for i, h := range hs {
				sy, dy := absY(h[0], 0), absY(h[1], 0)
				if i == 0 {
					sy = absY(h[0], e.fromY)
				}
				if i == len(hs)-1 {
					dy = absY(h[1], e.toY)
				}
				if sy == dy {
					continue
				}
				x := trackX(h[0].layer, fmt.Sprintf("%s#%d", e.src, h[0].layer))
				pts = append(pts, circuit.Point{X: x, Y: sy}, circuit.Point{X: x, Y: dy})
			}
		}
		c.Wires[e.wire.ID].Route = pts
	}
}
