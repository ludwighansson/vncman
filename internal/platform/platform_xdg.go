//go:build unix && !darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ViewerInstallHint says how to install the default viewer.
const ViewerInstallHint = "install TigerVNC's viewer from your distribution, for example the tigervnc-viewer package"

// StateDir returns $XDG_STATE_HOME/vncman, by default
// ~/.local/state/vncman.
func StateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, AppName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", AppName), nil
}

// CacheDir returns $XDG_CACHE_HOME/vncman, by default ~/.cache/vncman.
func CacheDir() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, AppName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", AppName), nil
}

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return launch("xdg-open", url) }

// FindCommand returns the path of a viewer program on the PATH.
func FindCommand(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s was not found", name)
	}
	return path, nil
}
