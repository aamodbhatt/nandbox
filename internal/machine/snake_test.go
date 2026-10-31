package machine

import (
	"testing"

	"github.com/aamodbhatt/nandbox/internal/asm"
	"github.com/aamodbhatt/nandbox/internal/isa"
	"github.com/aamodbhatt/nandbox/programs"
)

func assembleB(b *testing.B, name string) *asm.Program {
	b.Helper()
	src, err := programs.Source(name)
	if err != nil {
		b.Fatal(err)
	}
	p, err := asm.Assemble(src)
	if err != nil {
		b.Fatal(err)
	}
	return p
}

func source(t *testing.T, name string) string {
	t.Helper()
	src, err := programs.Source(name)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// console abstracts the gate-level machine and the reference emulator so
// the same scripted player drives both.
type console interface {
	step() error
	ram(addr int) uint8
	vram(addr int) uint8
	setJoy(mask uint8) error
	leds() uint8
	pc() uint8
}

type gateConsole struct{ m *Machine }

func (g gateConsole) step() error             { return g.m.Step() }
func (g gateConsole) ram(a int) uint8         { return uint8(g.m.RAM()[a]) }
func (g gateConsole) vram(a int) uint8        { return uint8(g.m.VRAM()[a]) }
func (g gateConsole) setJoy(mask uint8) error { return g.m.SetJoystick(mask) }
func (g gateConsole) leds() uint8             { return g.m.LEDs() }
func (g gateConsole) pc() uint8               { return g.m.PC() }

type refConsole struct{ m *isa.Machine }

func (r refConsole) step() error             { r.m.Step(); return nil }
func (r refConsole) ram(a int) uint8         { return r.m.RAM[a] }
func (r refConsole) vram(a int) uint8        { return r.m.VRAM[a] }
func (r refConsole) setJoy(mask uint8) error { r.m.Joy = mask; return nil }
func (r refConsole) leds() uint8             { return r.m.LEDs }
func (r refConsole) pc() uint8               { return r.m.PC }

const (
	dirUp, dirRight, dirDown, dirLeft = 1, 2, 3, 4
	pixFood, pixHead, pixCrash        = 5, 6, 7
)

var joyFor = map[int]uint8{dirUp: isa.JoyUp, dirRight: isa.JoyRight, dirDown: isa.JoyDown, dirLeft: isa.JoyLeft}

func opposite(d int) int { return (d+1)%4 + 1 }

func move(pos uint8, d int) uint8 {
	switch d {
	case dirUp:
		return pos - 16
	case dirDown:
		return pos + 16
	case dirLeft:
		return pos&0xF0 | (pos-1)&0x0F
	}
	return pos&0xF0 | (pos+1)&0x0F
}

type player struct {
	t    *testing.T
	c    console
	syms map[string]int
}

// waitMove steps until the head has moved and the game is back at the top
// of its loop (or has crashed), with a budget.
func (p *player) waitMove() {
	p.t.Helper()
	start, moved := p.c.ram(p.syms["head"]), false
	for range 4000 {
		if err := p.c.step(); err != nil {
			p.t.Fatal(err)
		}
		moved = moved || p.c.ram(p.syms["head"]) != start
		if p.c.leds() == 0xFF || moved && int(p.c.pc()) == p.syms["loop"] {
			return
		}
	}
	p.t.Fatal("snake stopped moving")
}

func wrapDelta(from, to uint8) int {
	d := (int(to) - int(from) + 16) % 16
	if d > 8 {
		d -= 16
	}
	return d
}

// chase picks a safe direction that closes in on the food.
func (p *player) chase() int {
	head, cur := p.c.ram(p.syms["head"]), int(p.c.ram(p.syms["dir"]))
	food := uint8(0)
	for a := range 256 {
		if p.c.vram(a) == pixFood {
			food = uint8(a)
		}
	}
	dx, dy := wrapDelta(head&15, food&15), wrapDelta(head>>4, food>>4)
	var prefs []int
	if dx > 0 {
		prefs = append(prefs, dirRight)
	} else if dx < 0 {
		prefs = append(prefs, dirLeft)
	}
	if dy > 0 {
		prefs = append(prefs, dirDown)
	} else if dy < 0 {
		prefs = append(prefs, dirUp)
	}
	prefs = append(prefs, cur, dirUp, dirRight, dirDown, dirLeft)
	for _, d := range prefs {
		if d == opposite(cur) {
			continue
		}
		if v := p.c.vram(int(move(head, d))); v == 0 || v == pixFood {
			return d
		}
	}
	return cur
}

func (p *player) score() uint8 { return p.c.ram(p.syms["score"]) }

// playSnake eats three pieces of food, crashes on purpose, and restarts.
func playSnake(t *testing.T, c console, syms map[string]int) {
	p := &player{t: t, c: c, syms: syms}
	for range 2000 {
		if c.vram(0x88) == pixHead && c.vram(0x86) == dirRight {
			break
		}
		if err := c.step(); err != nil {
			t.Fatal(err)
		}
	}
	if c.vram(0x88) != pixHead {
		t.Fatal("snake never appeared")
	}
	for moves := 0; p.score() < 3; moves++ {
		if moves > 300 {
			t.Fatalf("no third food after 300 moves (score %x)", p.score())
		}
		if err := c.setJoy(joyFor[p.chase()]); err != nil {
			t.Fatal(err)
		}
		p.waitMove()
		if c.leds() == 0xFF {
			t.Fatal("snake crashed while chasing food")
		}
	}
	length := 0
	for a := range 256 {
		if v := c.vram(a); v >= dirUp && v <= dirLeft || v == pixHead {
			length++
		}
	}
	if length != 6 {
		t.Fatalf("snake length %d after eating 3, want 6", length)
	}
	// Go straight twice, then U-turn into our own neck.
	_ = c.setJoy(0)
	p.waitMove()
	p.waitMove()
	d := int(c.ram(syms["dir"]))
	side := dirUp
	if d == dirUp || d == dirDown {
		side = dirLeft
	}
	for _, turn := range []int{side, opposite(d), opposite(side)} {
		_ = c.setJoy(joyFor[turn])
		p.waitMove()
	}
	if c.leds() != 0xFF || c.vram(int(c.ram(syms["head"]))) != pixCrash {
		t.Fatalf("expected a crash: leds=%#x", c.leds())
	}
	// Release, press any key, and a fresh game starts.
	_ = c.setJoy(0)
	for range 50 {
		_ = c.step()
	}
	_ = c.setJoy(isa.JoyFire)
	for range 3000 {
		_ = c.step()
		if c.leds() == 0 && c.vram(0x88) == pixHead {
			break
		}
	}
	if c.leds() != 0 || p.score() != 0 || c.vram(0x88) != pixHead {
		t.Fatalf("game did not restart: leds=%#x score=%x", c.leds(), p.score())
	}
}

func TestSnakeOnReferenceEmulator(t *testing.T) {
	p := assemble(t, source(t, "snake"), asm.WithDefine("DELAY", 2), asm.WithDefine("SPIN", 2))
	playSnake(t, refConsole{isa.NewMachine(p.Code, p.Data)}, p.Symbols)
}

func TestSnakeOnGateLevelCPU(t *testing.T) {
	p := assemble(t, source(t, "snake"), asm.WithDefine("DELAY", 2), asm.WithDefine("SPIN", 2))
	playSnake(t, gateConsole{build(t, p.Code, p.Data)}, p.Symbols)
}
