package layout_test

import (
	"testing"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/hdl"
)

// Every chip of the computer is laid out without overlapping parts, and
// every stored route is orthogonal and starts and ends on its pins.
func TestLayoutsAreCleanForEveryChip(t *testing.T) {
	d, _ := hdl.NewComputer(hdl.Program{})
	for _, cid := range d.SortedChipIDs() {
		c := d.Chips[cid]
		type box struct{ x0, y0, x1, y1 int }
		var boxes []box
		var ids []string
		for _, pid := range c.SortedPartIDs() {
			p := c.Parts[pid]
			s, err := d.Shape(p)
			if err != nil {
				t.Fatal(err)
			}
			b := box{p.X, p.Y, p.X + s.W, p.Y + s.H}
			for i, o := range boxes {
				if b.x0 < o.x1 && o.x0 < b.x1 && b.y0 < o.y1 && o.y0 < b.y1 {
					t.Errorf("%s: parts %s and %s overlap", cid, pid, ids[i])
				}
			}
			boxes = append(boxes, b)
			ids = append(ids, pid)
		}
		for _, wid := range c.SortedWireIDs() {
			w := c.Wires[wid]
			a, err := d.PinOf(c, w.From)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := d.PinOf(c, w.To)
			from, to := c.Parts[w.From.Part], c.Parts[w.To.Part]
			pts := append([]circuit.Point{{X: from.X + a.X, Y: from.Y + a.Y}}, w.Route...)
			pts = append(pts, circuit.Point{X: to.X + b.X, Y: to.Y + b.Y})
			for i := 1; i < len(pts); i++ {
				if pts[i].X != pts[i-1].X && pts[i].Y != pts[i-1].Y {
					t.Errorf("%s: wire %s has a diagonal segment %v -> %v", cid, wid, pts[i-1], pts[i])
					break
				}
			}
		}
	}
}

func TestLayoutIsDeterministic(t *testing.T) {
	a, _ := hdl.NewComputer(hdl.Program{Code: []uint16{1, 2, 3}})
	b, _ := hdl.NewComputer(hdl.Program{Code: []uint16{1, 2, 3}})
	for id, c := range a.Chips {
		for pid, p := range c.Parts {
			q := b.Chips[id].Parts[pid]
			if p.X != q.X || p.Y != q.Y {
				t.Fatalf("%s/%s placed differently across runs", id, pid)
			}
		}
	}
}
