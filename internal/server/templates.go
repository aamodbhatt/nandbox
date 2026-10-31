package server

import (
	"fmt"

	"github.com/aamodbhatt/nandbox/internal/asm"
	"github.com/aamodbhatt/nandbox/internal/circuit"
	"github.com/aamodbhatt/nandbox/internal/hdl"
	"github.com/aamodbhatt/nandbox/programs"
)

// Templates are the worlds a new room can start from.
var Templates = []string{"snake", "hello", "fib", "counter", "blank"}

// NewTemplate builds the design for a template: the CPU computer running a
// bundled program, or the standard chip library with an empty world.
func NewTemplate(name string) (*circuit.Design, error) {
	if name == "blank" {
		s := hdl.NewStd()
		s.Lib.Design.Chips[hdl.WorldID] = circuit.NewChipDef(hdl.WorldID, hdl.WorldID)
		s.Lib.Design.Root = hdl.WorldID
		return s.Lib.Design, nil
	}
	src, err := programs.Source(name)
	if err != nil {
		return nil, fmt.Errorf("unknown template %q", name)
	}
	prog, err := asm.Assemble(src)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	d, _ := hdl.NewComputer(hdl.Program{Code: prog.Code, Data: prog.Data})
	return d, nil
}
