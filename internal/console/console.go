// Package console embeds noVNC, the browser console (ADR 0004).
package console

import (
	"embed"
	"io/fs"
)

//go:embed novnc
var novnc embed.FS

// NoVNC returns the embedded noVNC release. Update it with
// scripts/update-novnc.sh.
func NoVNC() fs.FS {
	sub, err := fs.Sub(novnc, "novnc")
	if err != nil {
		panic(err)
	}
	return sub
}
