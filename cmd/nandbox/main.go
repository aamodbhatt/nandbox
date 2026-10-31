// Command nandbox serves the multiplayer logic sandbox, and benchmarks the
// simulator running Snake on the gate-level CPU.
//
//	nandbox                      serve on 127.0.0.1:8092
//	nandbox -addr :8080 -db x.db serve elsewhere, persisting rooms to x.db
//	nandbox bench -seconds 5     measure gate evaluations/s and CPU clock rate
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/aamodbhatt/nandbox/internal/asm"
	"github.com/aamodbhatt/nandbox/internal/hdl"
	"github.com/aamodbhatt/nandbox/internal/isa"
	"github.com/aamodbhatt/nandbox/internal/machine"
	"github.com/aamodbhatt/nandbox/internal/server"
	"github.com/aamodbhatt/nandbox/internal/sim"
	"github.com/aamodbhatt/nandbox/programs"
	"github.com/aamodbhatt/nandbox/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "bench" {
		if err := bench(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
			os.Exit(1)
		}
		return
	}
	if err := serve(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nandbox:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("nandbox", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8092", "listen address")
	dbPath := fs.String("db", "nandbox.db", "room database file (empty for memory only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var store *server.Store
	if *dbPath != "" {
		s, err := server.OpenStore(*dbPath)
		if err != nil {
			return err
		}
		defer s.Close()
		store = s
	}
	srv := server.New(store, web.Assets())
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shut)
	}()
	slog.Info("nandbox listening", "url", "http://"+*addr)
	err := httpSrv.ListenAndServe()
	srv.Shutdown()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// bench runs Snake with a random-turning player and reports simulator
// throughput for the event-driven and full-sweep engines.
func bench(args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	seconds := fs.Float64("seconds", 3, "wall time per engine")
	if err := fs.Parse(args); err != nil {
		return err
	}
	src, err := programs.Source("snake")
	if err != nil {
		return err
	}
	prog, err := asm.Assemble(src)
	if err != nil {
		return err
	}
	fmt.Printf("nandbox bench: snake.asm on the gate-level CPU (%s/%s, %d CPUs visible, Go %s)\n",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	for _, mode := range []sim.Mode{sim.ModeEvent, sim.ModeSweep} {
		m, err := machine.New(hdl.Program{Code: prog.Code, Data: prog.Data}, mode)
		if err != nil {
			return err
		}
		if mode == sim.ModeEvent {
			st := m.Sim.Netlist().Stats()
			fmt.Printf("design: %d NAND gates, %d flip-flops, %d memories, %d logic levels\n", st.Nands, st.DFFs, st.Memories, st.Levels)
		}
		r := rand.New(rand.NewPCG(1, 2))
		dirs := []uint8{isa.JoyUp, isa.JoyRight, isa.JoyDown, isa.JoyLeft}
		start := time.Now()
		cycles := 0
		for time.Since(start).Seconds() < *seconds {
			if cycles%20000 == 0 {
				if err := m.SetJoystick(dirs[r.IntN(4)]); err != nil {
					return err
				}
			}
			for range 1000 {
				if err := m.Cycle(); err != nil {
					return err
				}
			}
			cycles += 1000
		}
		el := time.Since(start).Seconds()
		name := map[sim.Mode]string{sim.ModeEvent: "event-driven", sim.ModeSweep: "full-sweep"}[mode]
		fmt.Printf("%-13s %8.1f kHz CPU clock  %7.1f M gate evals/s  %6.0f evals/cycle  (%d cycles in %.2fs)\n",
			name, float64(cycles)/el/1e3, float64(m.Sim.GateEvals)/el/1e6, float64(m.Sim.GateEvals)/float64(cycles), cycles, el)
	}
	return nil
}
