// Package programs embeds the bundled NB8 assembly programs.
package programs

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.asm
var files embed.FS

// Source returns the assembly source of a bundled program ("snake", "fib", ...).
func Source(name string) (string, error) {
	b, err := files.ReadFile(name + ".asm")
	if err != nil {
		return "", fmt.Errorf("no bundled program %q (have %s)", name, strings.Join(Names(), ", "))
	}
	return string(b), nil
}

// Names lists the bundled programs.
func Names() []string {
	entries, _ := files.ReadDir(".")
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".asm"))
	}
	sort.Strings(names)
	return names
}
