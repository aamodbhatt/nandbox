// Package web embeds the browser editor. The TypeScript in src/ is bundled
// into dist/ by `go generate ./web` using esbuild's Go API, so no Node.js
// toolchain is needed to build or run nandbox.
package web

import (
	"embed"
	"io/fs"
)

//go:generate go run ./build

//go:embed dist
var dist embed.FS

// Assets returns the bundled editor (index.html, app.js, style.css).
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed directive guarantees dist exists
	}
	return sub
}
