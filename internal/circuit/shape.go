package circuit

import (
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Dir is the electrical direction of a pin as seen from outside its part.
type Dir uint8

// Pin directions.
const (
	DirIn  Dir = 0
	DirOut Dir = 1
)

// PinShape describes one pin: its name, width and position relative to the
// part's top-left corner. Inputs sit on the left edge, outputs on the right.
type PinShape struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Dir   Dir    `json:"dir"`
	Width int    `json:"width"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}

// Shape is a part's footprint on the grid and its pins.
type Shape struct {
	W    int        `json:"w"`
	H    int        `json:"h"`
	Pins []PinShape `json:"pins"`
}

// Pin returns the named pin, if any.
func (s Shape) Pin(name string) (PinShape, bool) {
	for _, p := range s.Pins {
		if p.Name == name {
			return p, true
		}
	}
	return PinShape{}, false
}

func in(name string, w, x, y int) PinShape {
	return PinShape{Name: name, Label: name, Dir: DirIn, Width: w, X: x, Y: y}
}

func out(name string, w, x, y int) PinShape {
	return PinShape{Name: name, Label: name, Dir: DirOut, Width: w, X: x, Y: y}
}

// SegmentNames are the seven-segment display inputs, top segment first.
var SegmentNames = [7]string{"a", "b", "c", "d", "e", "f", "g"}

// JoystickPins are the joystick outputs in bit order of the input port.
var JoystickPins = [5]string{"up", "down", "left", "right", "fire"}

// EffectiveTaps returns a splitter's taps, defaulting to one tap per bit.
func (p *Part) EffectiveTaps() []Tap {
	if len(p.Taps) > 0 {
		return p.Taps
	}
	taps := make([]Tap, p.Width)
	for i := range taps {
		taps[i] = Tap{Lo: i, Hi: i + 1}
	}
	return taps
}

// TapLabel renders a tap range the way schematics do: "3" or "7:0".
func TapLabel(t Tap) string {
	if t.Hi-t.Lo == 1 {
		return strconv.Itoa(t.Lo)
	}
	return strconv.Itoa(t.Hi-1) + ":" + strconv.Itoa(t.Lo)
}

// Shape computes the footprint and pins of a part. Chip instances derive
// their pins from the In/Out parts of their definition, ordered by position.
func (d *Design) Shape(p *Part) (Shape, error) {
	switch p.Kind {
	case KindNand:
		return Shape{W: 4, H: 4, Pins: []PinShape{in("a", 1, 0, 1), in("b", 1, 0, 3), out("y", 1, 4, 2)}}, nil
	case KindDFF:
		return Shape{W: 4, H: 4, Pins: []PinShape{in("d", 1, 0, 1), in("clk", 1, 0, 3), out("q", 1, 4, 1)}}, nil
	case KindConst:
		return Shape{W: 2, H: 2, Pins: []PinShape{out("q", p.Width, 2, 1)}}, nil
	case KindIn:
		return Shape{W: 4, H: 2, Pins: []PinShape{out("q", p.Width, 4, 1)}}, nil
	case KindOut:
		return Shape{W: 4, H: 2, Pins: []PinShape{in("a", p.Width, 0, 1)}}, nil
	case KindClock, KindSwitch, KindButton:
		return Shape{W: 4, H: 2, Pins: []PinShape{out("q", 1, 4, 1)}}, nil
	case KindJoystick:
		s := Shape{W: 8, H: 10}
		for i, n := range JoystickPins {
			s.Pins = append(s.Pins, out(n, 1, 8, 2*i+1))
		}
		return s, nil
	case KindLED:
		return Shape{W: 2, H: 2, Pins: []PinShape{in("a", 1, 0, 1)}}, nil
	case KindSeg7:
		s := Shape{W: 8, H: 14}
		for i, n := range SegmentNames {
			s.Pins = append(s.Pins, in(n, 1, 0, 2*i+1))
		}
		return s, nil
	case KindSplitter:
		return splitterShape(p), nil
	case KindRAM:
		return Shape{W: 10, H: 8, Pins: []PinShape{
			in("addr", p.AddrBits, 0, 1), in("din", p.DataBits, 0, 3), in("we", 1, 0, 5), in("clk", 1, 0, 7),
			out("dout", p.DataBits, 10, 1)}}, nil
	case KindROM:
		return Shape{W: 10, H: 6, Pins: []PinShape{
			in("addr", p.AddrBits, 0, 1), in("en", 1, 0, 3), in("clk", 1, 0, 5),
			out("dout", p.DataBits, 10, 1)}}, nil
	case KindMatrix:
		n := 2*MatrixSide + 4
		return Shape{W: n, H: n, Pins: []PinShape{
			in("addr", 8, 0, 1), in("din", 8, 0, 3), in("we", 1, 0, 5), in("clk", 1, 0, 7),
			out("dout", 8, n, 1)}}, nil
	case KindNote:
		return Shape{W: max(4, (6*utf8.RuneCountInString(p.Label)+29)/10), H: 2}, nil
	case KindChip:
		def, err := d.Chip(p.Chip)
		if err != nil {
			return Shape{}, err
		}
		return ChipShape(def), nil
	}
	return Shape{}, fmt.Errorf("unknown part kind %q", p.Kind)
}

func splitterShape(p *Part) Shape {
	taps := p.EffectiveTaps()
	h := max(2, 2*len(taps))
	busX, tapX, tapDir, busDir := 0, 2, DirOut, DirIn
	if p.Flip {
		busX, tapX, tapDir, busDir = 2, 0, DirIn, DirOut
	}
	s := Shape{W: 2, H: h}
	s.Pins = append(s.Pins, PinShape{Name: "bus", Label: "", Dir: busDir, Width: p.Width, X: busX, Y: h / 2})
	for i, t := range taps {
		s.Pins = append(s.Pins, PinShape{Name: "t" + strconv.Itoa(i), Label: TapLabel(t), Dir: tapDir, Width: t.Hi - t.Lo, X: tapX, Y: 2*i + 1})
	}
	return s
}

// Interface returns a chip's In and Out parts in pin order (top to bottom,
// then left to right, then by ID).
func (c *ChipDef) Interface() (ins, outs []*Part) {
	for _, id := range c.SortedPartIDs() {
		p := c.Parts[id]
		switch p.Kind {
		case KindIn:
			ins = append(ins, p)
		case KindOut:
			outs = append(outs, p)
		}
	}
	byPos := func(ps []*Part) {
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].Y != ps[j].Y {
				return ps[i].Y < ps[j].Y
			}
			if ps[i].X != ps[j].X {
				return ps[i].X < ps[j].X
			}
			return ps[i].ID < ps[j].ID
		})
	}
	byPos(ins)
	byPos(outs)
	return ins, outs
}

// ChipShape is the symbol of a chip instance. Pins are named by the ID of the
// In/Out part they expose, so renaming a pin label never breaks wires.
func ChipShape(c *ChipDef) Shape {
	ins, outs := c.Interface()
	maxIn, maxOut := 0, 0
	for _, p := range ins {
		maxIn = max(maxIn, utf8.RuneCountInString(p.Name()))
	}
	for _, p := range outs {
		maxOut = max(maxOut, utf8.RuneCountInString(p.Name()))
	}
	w := max(6, (6*(maxIn+maxOut)+8*utf8.RuneCountInString(c.Name)+39)/10)
	if w%2 == 1 {
		w++
	}
	h := max(4, 2*max(len(ins), len(outs)))
	s := Shape{W: w, H: h}
	off := (h - 2*len(ins)) / 2
	for i, p := range ins {
		s.Pins = append(s.Pins, PinShape{Name: p.ID, Label: p.Name(), Dir: DirIn, Width: p.Width, X: 0, Y: off + 2*i + 1})
	}
	off = (h - 2*len(outs)) / 2
	for i, p := range outs {
		s.Pins = append(s.Pins, PinShape{Name: p.ID, Label: p.Name(), Dir: DirOut, Width: p.Width, X: w, Y: off + 2*i + 1})
	}
	return s
}
