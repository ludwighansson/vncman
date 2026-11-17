// Package console embeds noVNC, the browser console (ADR 0004).
package console

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed novnc
var novnc embed.FS

// noVNC keeps its dependencies in vendor/, but Go module zips leave out every
// path with a vendor directory in it (golang.org/issue/31562), so builds made
// with `go install` would lack them. The repository stores that directory as
// storedVendor, and NoVNC serves it under its original name.
const (
	vendor       = "vendor"
	storedVendor = "third_party"
)

// NoVNC returns the embedded noVNC release. Update it with
// scripts/update-novnc.sh.
func NoVNC() fs.FS {
	sub, err := fs.Sub(novnc, "novnc")
	if err != nil {
		panic(err)
	}
	return vendorFS{sub}
}

// vendorFS opens vendor/... from storedVendor/...
type vendorFS struct{ fs.FS }

func (f vendorFS) Open(name string) (fs.File, error) {
	if name != vendor && !strings.HasPrefix(name, vendor+"/") {
		return f.FS.Open(name)
	}
	file, err := f.FS.Open(storedVendor + strings.TrimPrefix(name, vendor))
	if pe, ok := err.(*fs.PathError); ok {
		pe.Path = name
	}
	return file, err
}
