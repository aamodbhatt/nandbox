package server

import (
	"errors"
	"fmt"

	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// Op is one edit to a design. Ops are applied by the server in a single
// order (the room's sequence), and every applied op yields its inverse so
// undo is just "apply the inverse as a new op".
type Op struct {
	T     string           `json:"t"` // addPart removePart movePart addWire removeWire setLabel setRoute addChip removeChip batch
	Chip  string           `json:"chip,omitempty"`
	ID    string           `json:"id,omitempty"`
	Part  *circuit.Part    `json:"part,omitempty"`
	Wire  *circuit.Wire    `json:"wire,omitempty"`
	X     int              `json:"x"`
	Y     int              `json:"y"`
	Label string           `json:"label,omitempty"`
	Route []circuit.Point  `json:"route,omitempty"`
	Ops   []Op             `json:"ops,omitempty"`
	Def   *circuit.ChipDef `json:"def,omitempty"`
}

// Apply mutates d by op and returns the op that undoes it. On error d is unchanged.
func Apply(d *circuit.Design, op Op) (Op, error) {
	if op.T == "batch" {
		var inv []Op
		for i, sub := range op.Ops {
			undo, err := Apply(d, sub)
			if err != nil {
				for j := len(inv) - 1; j >= 0; j-- {
					_, _ = Apply(d, inv[j])
				}
				return Op{}, fmt.Errorf("step %d: %w", i+1, err)
			}
			inv = append(inv, undo)
		}
		rev := make([]Op, len(inv))
		for i, o := range inv {
			rev[len(inv)-1-i] = o
		}
		return Op{T: "batch", Ops: rev}, nil
	}
	switch op.T {
	case "addChip":
		return addChip(d, op.Def)
	case "removeChip":
		return removeChip(d, op.ID)
	}
	c, ok := d.Chips[op.Chip]
	if !ok {
		return Op{}, fmt.Errorf("unknown chip %q", op.Chip)
	}
	switch op.T {
	case "addPart":
		p := op.Part
		if p == nil || p.ID == "" {
			return Op{}, errors.New("addPart needs a part with an ID")
		}
		if _, dup := c.Parts[p.ID]; dup {
			return Op{}, fmt.Errorf("part %q already exists", p.ID)
		}
		if err := d.ValidatePart(p); err != nil {
			return Op{}, err
		}
		if p.Kind == circuit.KindChip && d.Contains(p.Chip, c.ID) {
			return Op{}, fmt.Errorf("chip %s cannot contain itself", p.Chip)
		}
		if (p.Kind == circuit.KindIn || p.Kind == circuit.KindOut) && isInstantiated(d, c.ID) {
			return Op{}, errors.New("changing the pins of a chip that is in use is not supported")
		}
		c.Parts[p.ID] = p.Clone()
		return Op{T: "removePart", Chip: c.ID, ID: p.ID}, nil
	case "removePart":
		p, ok := c.Parts[op.ID]
		if !ok {
			return Op{}, fmt.Errorf("unknown part %q", op.ID)
		}
		if (p.Kind == circuit.KindIn || p.Kind == circuit.KindOut) && isInstantiated(d, c.ID) {
			return Op{}, errors.New("changing the pins of a chip that is in use is not supported")
		}
		inv := Op{T: "batch", Ops: []Op{{T: "addPart", Chip: c.ID, Part: p.Clone()}}}
		for _, wid := range c.SortedWireIDs() {
			if w := c.Wires[wid]; w.From.Part == p.ID || w.To.Part == p.ID {
				inv.Ops = append(inv.Ops, Op{T: "addWire", Chip: c.ID, Wire: w.Clone()})
				delete(c.Wires, wid)
			}
		}
		delete(c.Parts, p.ID)
		return inv, nil
	case "movePart":
		p, ok := c.Parts[op.ID]
		if !ok {
			return Op{}, fmt.Errorf("unknown part %q", op.ID)
		}
		inv := Op{T: "batch", Ops: []Op{{T: "movePart", Chip: c.ID, ID: p.ID, X: p.X, Y: p.Y}}}
		for _, wid := range c.SortedWireIDs() {
			if w := c.Wires[wid]; (w.From.Part == p.ID || w.To.Part == p.ID) && len(w.Route) > 0 {
				inv.Ops = append(inv.Ops, Op{T: "setRoute", Chip: c.ID, ID: wid, Route: w.Route})
				w.Route = nil // stored routes no longer fit; the editor auto-routes
			}
		}
		p.X, p.Y = op.X, op.Y
		return inv, nil
	case "setRoute":
		w, ok := c.Wires[op.ID]
		if !ok {
			return Op{}, fmt.Errorf("unknown wire %q", op.ID)
		}
		old := w.Route
		w.Route = append([]circuit.Point(nil), op.Route...)
		return Op{T: "setRoute", Chip: c.ID, ID: w.ID, Route: old}, nil
	case "addWire":
		w := op.Wire
		if w == nil {
			return Op{}, errors.New("addWire needs a wire")
		}
		if _, dup := c.Wires[w.ID]; dup {
			return Op{}, fmt.Errorf("wire %q already exists", w.ID)
		}
		if err := d.ValidateWire(c, w); err != nil {
			return Op{}, err
		}
		for _, other := range c.Wires {
			if other.From == w.From && other.To == w.To || other.From == w.To && other.To == w.From {
				return Op{}, errors.New("those pins are already connected")
			}
		}
		c.Wires[w.ID] = w.Clone()
		return Op{T: "removeWire", Chip: c.ID, ID: w.ID}, nil
	case "removeWire":
		w, ok := c.Wires[op.ID]
		if !ok {
			return Op{}, fmt.Errorf("unknown wire %q", op.ID)
		}
		delete(c.Wires, op.ID)
		return Op{T: "addWire", Chip: c.ID, Wire: w}, nil
	case "setLabel":
		p, ok := c.Parts[op.ID]
		if !ok {
			return Op{}, fmt.Errorf("unknown part %q", op.ID)
		}
		if len(op.Label) > 64 {
			return Op{}, errors.New("labels are limited to 64 characters")
		}
		old := p.Label
		p.Label = op.Label
		return Op{T: "setLabel", Chip: c.ID, ID: p.ID, Label: old}, nil
	}
	return Op{}, fmt.Errorf("unknown op %q", op.T)
}

func isInstantiated(d *circuit.Design, chip string) bool {
	for _, c := range d.Chips {
		for _, p := range c.Parts {
			if p.Kind == circuit.KindChip && p.Chip == chip {
				return true
			}
		}
	}
	return false
}

// addChip adds a new chip definition (how "create chip from selection" lands).
func addChip(d *circuit.Design, def *circuit.ChipDef) (Op, error) {
	if def == nil || def.ID == "" || def.Name == "" {
		return Op{}, errors.New("addChip needs a definition with an ID and a name")
	}
	if _, dup := d.Chips[def.ID]; dup {
		return Op{}, fmt.Errorf("chip %q already exists", def.ID)
	}
	c := def.Clone()
	if c.Parts == nil {
		c.Parts = map[string]*circuit.Part{}
	}
	if c.Wires == nil {
		c.Wires = map[string]*circuit.Wire{}
	}
	d.Chips[c.ID] = c
	if err := d.Validate(); err != nil {
		delete(d.Chips, c.ID)
		return Op{}, fmt.Errorf("new chip %s: %w", c.Name, err)
	}
	return Op{T: "removeChip", ID: c.ID}, nil
}

// removeChip deletes a definition that nothing instantiates any more.
func removeChip(d *circuit.Design, id string) (Op, error) {
	c, ok := d.Chips[id]
	if !ok {
		return Op{}, fmt.Errorf("unknown chip %q", id)
	}
	if id == d.Root || isInstantiated(d, id) {
		return Op{}, fmt.Errorf("chip %s is in use", c.Name)
	}
	delete(d.Chips, id)
	return Op{T: "addChip", Def: c}, nil
}
