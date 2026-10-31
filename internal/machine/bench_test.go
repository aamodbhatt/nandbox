package machine

import (
	"testing"

	"github.com/aamodbhatt/nandbox/internal/hdl"
	"github.com/aamodbhatt/nandbox/internal/isa"
	"github.com/aamodbhatt/nandbox/internal/sim"
)

// benchmarkSnake reports simulated CPU cycles per second and gate
// evaluations per second while snake.asm runs with a steering player.
func benchmarkSnake(b *testing.B, mode sim.Mode) {
	p := assembleB(b, "snake")
	m, err := New(hdl.Program{Code: p.Code, Data: p.Data}, mode)
	if err != nil {
		b.Fatal(err)
	}
	dirs := []uint8{isa.JoyUp, isa.JoyRight, isa.JoyDown, isa.JoyLeft}
	start := m.Sim.GateEvals
	b.ResetTimer()
	for i := range b.N {
		if i%20000 == 0 {
			_ = m.SetJoystick(dirs[(i/20000)%4])
		}
		if err := m.Cycle(); err != nil {
			b.Fatal(err)
		}
	}
	sec := b.Elapsed().Seconds()
	b.ReportMetric(float64(b.N)/sec/1e3, "kHz")
	b.ReportMetric(float64(m.Sim.GateEvals-start)/sec/1e6, "Mevals/s")
	b.ReportMetric(float64(m.Sim.GateEvals-start)/float64(b.N), "evals/cycle")
}

func BenchmarkSnakeEventDriven(b *testing.B) { benchmarkSnake(b, sim.ModeEvent) }
func BenchmarkSnakeFullSweep(b *testing.B)   { benchmarkSnake(b, sim.ModeSweep) }
