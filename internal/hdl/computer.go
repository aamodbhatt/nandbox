package hdl

import (
	"github.com/aamodbhatt/nandbox/internal/circuit"
)

// Part IDs of the computer's top-level devices, used by tests, the server
// and the benchmark to find them.
const (
	WorldID   = "World"
	PartCPU   = "cpu"
	PartROM   = "rom"
	PartRAM   = "ram"
	PartVRAM  = "screen"
	PartIO    = "io"
	PartClock = "clock"
	PartReset = "reset"
	PartJoy   = "joystick"
)

// Program is what the computer runs: ROM words and the initial RAM image.
type Program struct {
	Code []uint16
	Data []uint8
}

// NewComputer builds the standard library plus a "World" root chip: the CPU
// wired to its ROM, RAM, LED-matrix screen, I/O bus, joystick, score display
// and debug LEDs, with prog loaded.
func NewComputer(prog Program) (*circuit.Design, *Std) {
	s := NewStd()
	l := s.Lib
	l.Define(WorldID, func(b *Builder) {
		clk := Output(b.Device(&circuit.Part{ID: PartClock, Kind: circuit.KindClock, Label: "clock"}, nil), "q", 1)
		reset := Output(b.Device(&circuit.Part{ID: PartReset, Kind: circuit.KindButton, Label: "reset"}, nil), "q", 1)
		joyID := b.Device(&circuit.Part{ID: PartJoy, Kind: circuit.KindJoystick, Label: "joystick"}, nil)
		var joy Bus
		for _, pin := range circuit.JoystickPins {
			joy = append(joy, Output(joyID, pin, 1)...)
		}
		instr, rdata := b.Forward(16), b.Forward(8)
		ramWE, vramWE := b.Forward(1), b.Forward(1)
		cpu := b.UseAs(PartCPU, s.CPU, "CPU", Pins{"clk": clk, "reset": reset, "instr": instr, "rdata": rdata})
		words := make([]uint16, len(prog.Code))
		copy(words, prog.Code)
		rom := b.Device(&circuit.Part{ID: PartROM, Kind: circuit.KindROM, Label: "program ROM", AddrBits: 8, DataBits: 16, Words: words},
			Pins{"addr": cpu["pc"], "en": cpu["fetch"], "clk": clk})
		b.Resolve(instr, Output(rom, "dout", 16))
		init := make([]uint16, len(prog.Data))
		for i, v := range prog.Data {
			init[i] = uint16(v)
		}
		ram := b.Device(&circuit.Part{ID: PartRAM, Kind: circuit.KindRAM, Label: "RAM", AddrBits: 8, DataBits: 8, Words: init},
			Pins{"addr": cpu["addr"], "din": cpu["wdata"], "we": ramWE, "clk": clk})
		vram := b.Device(&circuit.Part{ID: PartVRAM, Kind: circuit.KindMatrix, Label: "screen"},
			Pins{"addr": cpu["addr"], "din": cpu["wdata"], "we": vramWE, "clk": clk})
		io := b.UseAs(PartIO, s.IOBus, "I/O", Pins{"clk": clk, "addr": cpu["addr"], "wdata": cpu["wdata"], "store": cpu["store"],
			"space": cpu["space"], "ram": Output(ram, "dout", 8), "vram": Output(vram, "dout", 8), "joy": joy})
		b.Resolve(rdata, io["rdata"])
		b.Resolve(ramWE, io["ramWE"])
		b.Resolve(vramWE, io["vramWE"])
		// IDs sort high digit first, which is the order displays list them in.
		for digit, nib := range []Bus{io["score"].Slice(4, 8), io["score"].Slice(0, 4)} {
			name := []string{"hi", "lo"}[digit]
			segs := b.Call(s.Hex7Seg, "hex "+name, nib)
			ins := Pins{}
			for i, seg := range circuit.SegmentNames {
				ins[seg] = segs[i]
			}
			b.Device(&circuit.Part{ID: "score_" + name, Kind: circuit.KindSeg7, Label: "score " + name}, ins)
		}
		for i := range 8 {
			b.Device(&circuit.Part{ID: "led" + string(rune('0'+i)), Kind: circuit.KindLED}, Pins{"a": io["leds"].Slice(7-i, 8-i)})
		}
	})
	l.Design.Root = WorldID
	return l.Design, s
}
