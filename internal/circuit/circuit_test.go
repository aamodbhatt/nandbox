package circuit

import (
	"strings"
	"testing"
)

func design() *Design {
	inner := NewChipDef("inv", "Inv")
	inner.Parts["a"] = &Part{ID: "a", Kind: KindIn, Width: 1, Label: "a"}
	inner.Parts["longer_name"] = &Part{ID: "longer_name", Kind: KindOut, Width: 1, Y: 2}
	inner.Parts["n"] = &Part{ID: "n", Kind: KindNand}
	world := NewChipDef("w", "World")
	world.Parts["u"] = &Part{ID: "u", Kind: KindChip, Chip: "inv"}
	world.Parts["k"] = &Part{ID: "k", Kind: KindConst, Width: 4, Value: 9}
	world.Parts["s"] = &Part{ID: "s", Kind: KindSplitter, Width: 4, Taps: []Tap{{Lo: 0, Hi: 1}, {Lo: 1, Hi: 4}}}
	world.Wires["w1"] = &Wire{ID: "w1", From: PinRef{"k", "q"}, To: PinRef{"s", "bus"}}
	world.Wires["w2"] = &Wire{ID: "w2", From: PinRef{"s", "t0"}, To: PinRef{"u", "a"}}
	return &Design{Root: "w", Chips: map[string]*ChipDef{"inv": inner, "w": world}}
}

func TestValidDesignPasses(t *testing.T) {
	if err := design().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidationCatchesBrokenInvariants(t *testing.T) {
	cases := map[string]func(d *Design){
		"does not fit":         func(d *Design) { d.Chips["w"].Parts["k"].Value = 16 },
		"joins 3-bit pin":      func(d *Design) { d.Chips["w"].Wires["w2"].From.Pin = "t1" },
		"has no pin":           func(d *Design) { d.Chips["w"].Wires["w2"].To.Pin = "zz" },
		"contains itself":      func(d *Design) { d.Chips["inv"].Parts["loop"] = &Part{ID: "loop", Kind: KindChip, Chip: "w"} },
		"outside bus":          func(d *Design) { d.Chips["w"].Parts["s"].Taps[1].Hi = 5 },
		"unknown chip":         func(d *Design) { d.Chips["w"].Parts["u"].Chip = "nope" },
		"root chip":            func(d *Design) { d.Root = "missing" },
		"width does not apply": func(d *Design) { d.Chips["inv"].Parts["n"].Width = 2 },
	}
	for want, breakIt := range cases {
		d := design()
		breakIt(d)
		if err := d.Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestChipShapeOrdersPinsByPosition(t *testing.T) {
	d := design()
	s := ChipShape(d.Chips["inv"])
	if len(s.Pins) != 2 || s.Pins[0].Name != "a" || s.Pins[0].X != 0 || s.Pins[1].Name != "longer_name" || s.Pins[1].X != s.W {
		t.Fatalf("unexpected pins %+v", s.Pins)
	}
	if s.W%2 != 0 || s.H < 4 {
		t.Fatalf("symbol %dx%d is not on the even grid", s.W, s.H)
	}
	clone := d.Clone()
	clone.Chips["w"].Parts["s"].Taps[0].Hi = 2
	if d.Chips["w"].Parts["s"].Taps[0].Hi != 1 {
		t.Fatal("Clone shares tap slices with the original")
	}
}
