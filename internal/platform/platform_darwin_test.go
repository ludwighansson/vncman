//go:build darwin

package platform

import "testing"

func TestMacDirectories(t *testing.T) {
	t.Setenv("HOME", "/Users/op")
	if got, _ := StateDir(); got != "/Users/op/Library/Application Support/vncman" {
		t.Errorf("state: got %q", got)
	}
	if got, _ := CacheDir(); got != "/Users/op/Library/Caches/vncman" {
		t.Errorf("cache: got %q", got)
	}
}
