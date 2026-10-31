// Package machine runs the gate-level nandbox computer headlessly and reads
// its architectural state (registers, flags, memories) back out of the
// simulated wires. Tests, the benchmark and the server's showcase use it.
package machine

import (
	"fmt"

	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/hdl"
	"github.com/aamodbhatt/nandbox/internal/sim"
)

// Machine is a compiled computer running a program.
type Machine struct {
	Design *circuit.Design
	Sim    *sim.Sim
	world  *sim.Instance
	cpu    *sim.Instance
	regs   [4][]int32
	pc     []int32
	flags  [3][]int32 // Z, N, C
}

// New builds, compiles and powers on the computer with prog loaded.
func New(prog hdl.Program, mode sim.Mode) (*Machine, error) {
	d, _ := hdl.NewComputer(prog)
	nl, err := sim.Compile(d)
	if err != nil {
		return nil, fmt.Errorf("compile computer: %w", err)
	}
	m := &Machine{Design: d, Sim: sim.New(nl, mode), world: nl.Root()}
	m.cpu = m.world.Find([]string{hdl.PartCPU})
	regFileID := m.partByLabel("CPU", "regs")
	regFile := m.cpu.Children[regFileID]
	for i := range m.regs {
		m.regs[i] = regFile.Pin(m.partByLabel("RegFile", fmt.Sprintf("r%d", i)), "out")
	}
	m.pc = m.cpu.Pin(m.partByLabel("CPU", "pc"), "out")
	flagsID := m.partByLabel("CPU", "flags")
	for i, f := range []string{"Z", "N", "C"} {
		m.flags[i] = m.cpu.Pin(flagsID, f)
	}
	return m, nil
}

func (m *Machine) partByLabel(chip, label string) string {
	for _, id := range m.Design.Chips[chip].SortedPartIDs() {
		if p := m.Design.Chips[chip].Parts[id]; p.Kind == circuit.KindChip && p.Label == label {
			return id
		}
	}
	panic(fmt.Sprintf("machine: chip %s has no part labelled %q", chip, label))
}

// Cycle runs one clock cycle.
func (m *Machine) Cycle() error { return m.Sim.Cycle() }

// Step runs one instruction (a fetch cycle and an execute cycle).
func (m *Machine) Step() error {
	if err := m.Sim.Cycle(); err != nil {
		return err
	}
	return m.Sim.Cycle()
}

// RunUntilHalt steps until the CPU executes HLT, or fails after maxSteps.
func (m *Machine) RunUntilHalt(maxSteps int) error {
	for range maxSteps {
		if m.Halted() {
			return nil
		}
		if err := m.Step(); err != nil {
			return err
		}
	}
	return fmt.Errorf("program did not halt within %d instructions", maxSteps)
}

func (m *Machine) bits(sigs []int32) uint8 { return uint8(m.Sim.Bits(sigs)) }

// Reg returns register r0..r3.
func (m *Machine) Reg(i int) uint8 { return m.bits(m.regs[i]) }

// PC returns the program counter.
func (m *Machine) PC() uint8 { return m.bits(m.pc) }

// Flags returns Z, N and C.
func (m *Machine) Flags() (z, n, c bool) {
	return m.bits(m.flags[0]) == 1, m.bits(m.flags[1]) == 1, m.bits(m.flags[2]) == 1
}

// Halted reports whether the instruction register holds HLT.
func (m *Machine) Halted() bool { return m.bits(m.world.Pin(hdl.PartCPU, "halted")) == 1 }

// Score returns the 7-segment score register.
func (m *Machine) Score() uint8 { return m.bits(m.world.Pin(hdl.PartIO, "score")) }

// LEDs returns the debug LED register.
func (m *Machine) LEDs() uint8 { return m.bits(m.world.Pin(hdl.PartIO, "leds")) }

// RAM returns the data RAM contents.
func (m *Machine) RAM() []uint16 { words, _, _ := m.Sim.Memory(hdl.PartRAM); return words }

// VRAM returns the LED matrix framebuffer (one byte per pixel).
func (m *Machine) VRAM() []uint16 { words, _, _ := m.Sim.Memory(hdl.PartVRAM); return words }

// SetJoystick drives the joystick lines from a bit mask (isa.Joy* bits).
func (m *Machine) SetJoystick(mask uint8) error {
	for i, pin := range circuit.JoystickPins {
		if err := m.Sim.Poke(hdl.PartJoy+"."+pin, mask>>uint(i)&1); err != nil {
			return err
		}
	}
	return m.Sim.Propagate()
}
