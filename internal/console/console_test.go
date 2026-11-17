package console

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// Go module zips drop every file below a directory named vendor, so
// `go install` builds would embed noVNC without it.
func TestNothingIsDroppedFromModuleZips(t *testing.T) {
	err := fs.WalkDir(novnc, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, elem := range strings.Split(p, "/") {
			if elem == vendor {
				t.Errorf("%s: module zips leave this out; store it under %s/ instead", p, storedVendor)
				return fs.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// importRE matches the module specifiers of static imports and re-exports,
// including those that span several lines.
var importRE = regexp.MustCompile(`(?m)^\s*(?:(?:import|export)\s[\w\s{},*$]*?from\s*|import\s*)["']([^"']+)["']`)

// Every module the browser loads must be embedded, at the path noVNC asks for.
func TestModuleImportsResolve(t *testing.T) {
	fsys := NoVNC()
	page, err := fs.ReadFile(fsys, "vnc.html")
	if err != nil {
		t.Fatal(err)
	}
	queue := []string{}
	for _, m := range importRE.FindAllStringSubmatch(string(page), -1) {
		queue = append(queue, path.Clean(m[1]))
	}
	if len(queue) == 0 {
		t.Fatal("vnc.html imports no modules")
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if seen[file] {
			continue
		}
		seen[file] = true
		src, err := fs.ReadFile(fsys, file)
		if err != nil {
			t.Errorf("%s isn't embedded: %v", file, err)
			continue
		}
		for _, m := range importRE.FindAllStringSubmatch(string(src), -1) {
			spec := m[1]
			if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
				t.Errorf("%s imports %q, which the browser can't load", file, spec)
				continue
			}
			queue = append(queue, path.Join(path.Dir(file), spec))
		}
	}
	for _, want := range []string{"app/ui.js", "core/rfb.js", "vendor/pako/lib/zlib/inflate.js", "vendor/pako/lib/zlib/zstream.js"} {
		if !seen[want] {
			t.Errorf("the import graph misses %s; is the import pattern still right?", want)
		}
	}
}
