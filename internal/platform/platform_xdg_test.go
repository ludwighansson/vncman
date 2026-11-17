//go:build unix && !darwin

package platform

import "testing"

func TestXDGDirectories(t *testing.T) {
	t.Setenv("HOME", "/home/op")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	check := func(what string, f func() (string, error), want string) {
		t.Helper()
		got, err := f()
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", what, got, err, want)
		}
	}
	check("state", StateDir, "/home/op/.local/state/vncman")
	check("cache", CacheDir, "/home/op/.cache/vncman")

	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	t.Setenv("XDG_CACHE_HOME", "/xdg/cache")
	check("state", StateDir, "/xdg/state/vncman")
	check("cache", CacheDir, "/xdg/cache/vncman")

	// The XDG spec says relative paths are invalid and must be ignored.
	t.Setenv("XDG_STATE_HOME", "relative")
	check("state", StateDir, "/home/op/.local/state/vncman")
}
