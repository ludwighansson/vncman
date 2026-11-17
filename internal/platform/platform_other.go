//go:build !unix

package platform

import (
	"errors"
	"os/exec"
)

// VNCMan relies on ssh's ControlMaster, which Windows' OpenSSH lacks
// (ADR 0008). These stubs only keep the code compiling.
var errUnsupported = errors.New("VNCMan supports macOS and Linux only")

// ViewerInstallHint says how to install the default viewer.
const ViewerInstallHint = ""

// StateDir is not supported on this system.
func StateDir() (string, error) { return "", errUnsupported }

// CacheDir is not supported on this system.
func CacheDir() (string, error) { return "", errUnsupported }

// OpenURL is not supported on this system.
func OpenURL(string) error { return errUnsupported }

// FindCommand is not supported on this system.
func FindCommand(string) (string, error) { return "", errUnsupported }

// Detach does nothing on this system.
func Detach(*exec.Cmd) {}
