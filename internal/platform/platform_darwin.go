//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ViewerInstallHint says how to install the default viewer.
const ViewerInstallHint = "install TigerVNC with: brew install --cask tigervnc-viewer"

// StateDir returns ~/Library/Application Support/vncman.
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", AppName), nil
}

// CacheDir returns ~/Library/Caches/vncman.
func CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Caches", AppName), nil
}

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return launch("open", url) }

// FindCommand returns the path of a viewer program. TigerVNC's app doesn't
// put vncviewer on the PATH, so vncviewer is also looked for inside it.
func FindCommand(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if name == "vncviewer" {
		if path := tigerVNCBinary(); path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s was not found", name)
}

const tigerVNCBundleID = "com.tigervnc.tigervnc"

// tigerVNCBinary finds vncviewer inside TigerVNC.app, wherever it is
// installed.
func tigerVNCBinary() string {
	var apps []string
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	query := fmt.Sprintf("kMDItemCFBundleIdentifier == '%s'", tigerVNCBundleID)
	if out, err := exec.CommandContext(ctx, "mdfind", query).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				apps = append(apps, line)
			}
		}
	}
	apps = append(apps, "/Applications/TigerVNC.app")
	if home, err := os.UserHomeDir(); err == nil {
		apps = append(apps, filepath.Join(home, "Applications", "TigerVNC.app"))
	}
	for _, app := range apps {
		bin := filepath.Join(app, "Contents", "MacOS", "vncviewer")
		if info, err := os.Stat(bin); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return bin
		}
	}
	return ""
}
