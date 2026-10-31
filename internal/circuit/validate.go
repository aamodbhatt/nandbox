package circuit

import (
	"errors"
	"fmt"
)

// Validate checks every structural invariant of a design: references resolve,
// per-kind properties are in range, wires join existing pins of equal width
// and no chip contains itself. It returns all problems joined together.
func (d *Design) Validate() error {
	var errs []error
	if _, ok := d.Chips[d.Root]; !ok {
		errs = append(errs, fmt.Errorf("root chip %q does not exist", d.Root))
	}
	for _, cid := range d.SortedChipIDs() {
		c := d.Chips[cid]
		if c.ID != cid {
			errs = append(errs, fmt.Errorf("chip key %q holds chip with ID %q", cid, c.ID))
		}
		if c.Name == "" {
			errs = append(errs, fmt.Errorf("chip %q has no name", cid))
		}
		for _, pid := range c.SortedPartIDs() {
			p := c.Parts[pid]
			if p.ID != pid {
				errs = append(errs, fmt.Errorf("chip %q: part key %q holds part %q", cid, pid, p.ID))
			}
			if err := d.ValidatePart(p); err != nil {
				errs = append(errs, fmt.Errorf("chip %q: %w", cid, err))
			}
		}
		for _, wid := range c.SortedWireIDs() {
			if err := d.ValidateWire(c, c.Wires[wid]); err != nil {
				errs = append(errs, fmt.Errorf("chip %q: %w", cid, err))
			}
		}
	}
	if err := d.checkRecursion(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ValidatePart checks the per-kind properties of a single part.
func (d *Design) ValidatePart(p *Part) error {
	if p.ID == "" {
		return errors.New("part has an empty ID")
	}
	bad := func(format string, args ...any) error {
		return fmt.Errorf("part %q (%s): %s", p.ID, p.Kind, fmt.Sprintf(format, args...))
	}
	usesWidth := p.Kind == KindIn || p.Kind == KindOut || p.Kind == KindConst || p.Kind == KindSplitter
	if usesWidth && (p.Width < 1 || p.Width > MaxWidth) {
		return bad("width %d outside 1..%d", p.Width, MaxWidth)
	}
	if !usesWidth && p.Width != 0 {
		return bad("width does not apply")
	}
	if p.Kind != KindConst && p.Value != 0 {
		return bad("value does not apply")
	}
	if p.Kind != KindSplitter && (p.Flip || len(p.Taps) > 0) {
		return bad("flip/taps do not apply")
	}
	if p.Kind != KindChip && p.Chip != "" {
		return bad("chip reference does not apply")
	}
	isMem := p.Kind == KindRAM || p.Kind == KindROM
	if !isMem && (p.AddrBits != 0 || p.DataBits != 0) {
		return bad("addrBits/dataBits do not apply")
	}
	if !isMem && p.Kind != KindMatrix && len(p.Words) > 0 {
		return bad("words do not apply")
	}
	switch p.Kind {
	case KindNand, KindDFF, KindClock, KindSwitch, KindButton, KindJoystick, KindLED, KindSeg7, KindIn, KindOut, KindNote:
	case KindConst:
		if p.Value>>uint(p.Width) != 0 {
			return bad("value %d does not fit in %d bits", p.Value, p.Width)
		}
	case KindSplitter:
		if p.Width < 2 {
			return bad("a splitter needs a bus of at least 2 bits")
		}
		for _, t := range p.Taps {
			if t.Lo < 0 || t.Hi > p.Width || t.Lo >= t.Hi {
				return bad("tap [%d,%d) outside bus of width %d", t.Lo, t.Hi, p.Width)
			}
		}
	case KindRAM, KindROM:
		if p.AddrBits < 1 || p.AddrBits > MaxAddrBits {
			return bad("addrBits %d outside 1..%d", p.AddrBits, MaxAddrBits)
		}
		if p.DataBits < 1 || p.DataBits > MaxWidth {
			return bad("dataBits %d outside 1..%d", p.DataBits, MaxWidth)
		}
		if len(p.Words) > 1<<uint(p.AddrBits) {
			return bad("%d words do not fit in %d address bits", len(p.Words), p.AddrBits)
		}
		for i, w := range p.Words {
			if uint64(w)>>uint(p.DataBits) != 0 {
				return bad("word %d (0x%x) does not fit in %d bits", i, w, p.DataBits)
			}
		}
	case KindMatrix:
		if len(p.Words) > MatrixSide*MatrixSide {
			return bad("%d words exceed the %d pixels", len(p.Words), MatrixSide*MatrixSide)
		}
		for i, w := range p.Words {
			if w > 0xFF {
				return bad("pixel %d (0x%x) does not fit in 8 bits", i, w)
			}
		}
	case KindChip:
		if _, ok := d.Chips[p.Chip]; !ok {
			return bad("instantiates unknown chip %q", p.Chip)
		}
	default:
		return bad("unknown kind")
	}
	return nil
}

// ValidateWire checks that a wire joins two distinct existing pins of equal width.
func (d *Design) ValidateWire(c *ChipDef, w *Wire) error {
	if w.ID == "" {
		return errors.New("wire has an empty ID")
	}
	if w.From == w.To {
		return fmt.Errorf("wire %q connects pin %s to itself", w.ID, w.From)
	}
	a, err := d.PinOf(c, w.From)
	if err != nil {
		return fmt.Errorf("wire %q: %w", w.ID, err)
	}
	b, err := d.PinOf(c, w.To)
	if err != nil {
		return fmt.Errorf("wire %q: %w", w.ID, err)
	}
	if a.Width != b.Width {
		return fmt.Errorf("wire %q joins %d-bit pin %s to %d-bit pin %s", w.ID, a.Width, w.From, b.Width, w.To)
	}
	return nil
}

// PinOf resolves a pin reference inside chip c.
func (d *Design) PinOf(c *ChipDef, r PinRef) (PinShape, error) {
	p, ok := c.Parts[r.Part]
	if !ok {
		return PinShape{}, fmt.Errorf("unknown part %q", r.Part)
	}
	s, err := d.Shape(p)
	if err != nil {
		return PinShape{}, err
	}
	pin, ok := s.Pin(r.Pin)
	if !ok {
		return PinShape{}, fmt.Errorf("part %q has no pin %q", r.Part, r.Pin)
	}
	return pin, nil
}

// Contains reports whether chip outer instantiates chip inner, directly or
// transitively (or is inner itself).
func (d *Design) Contains(outer, inner string) bool {
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == inner {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		c, ok := d.Chips[id]
		if !ok {
			return false
		}
		for _, pid := range c.SortedPartIDs() {
			if p := c.Parts[pid]; p.Kind == KindChip && walk(p.Chip) {
				return true
			}
		}
		return false
	}
	return walk(outer)
}

func (d *Design) checkRecursion() error {
	for _, cid := range d.SortedChipIDs() {
		c := d.Chips[cid]
		for _, pid := range c.SortedPartIDs() {
			p := c.Parts[pid]
			if p.Kind == KindChip && d.Contains(p.Chip, cid) {
				return fmt.Errorf("chip %q contains itself through part %q (%s)", cid, pid, p.Chip)
			}
		}
	}
	return nil
}
