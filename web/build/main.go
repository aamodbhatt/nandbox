// Command build bundles the editor with esbuild's Go API: src/main.ts and
// its imports become dist/app.js, and static/ is copied next to it.
// Run it with `go generate ./web` from the repository root.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "web build:", err)
		os.Exit(1)
	}
}

func run() error {
	res := api.Build(api.BuildOptions{
		EntryPoints:       []string{"src/main.ts"},
		Outfile:           "dist/app.js",
		Bundle:            true,
		Write:             true,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		Target:            api.ES2022,
		Format:            api.FormatIIFE,
		LegalComments:     api.LegalCommentsNone,
		LogLevel:          api.LogLevelWarning,
	})
	if len(res.Errors) > 0 {
		return fmt.Errorf("%d esbuild error(s): %s", len(res.Errors), res.Errors[0].Text)
	}
	entries, err := os.ReadDir("static")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join("static", e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join("dist", e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
