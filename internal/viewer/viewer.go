// Package viewer starts the user's VNC viewer (ADR 0009).
package viewer

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ludwighansson/vncman/internal/limitbuf"
	"github.com/ludwighansson/vncman/internal/platform"
)

// Default runs TigerVNC's viewer. "::" means a TCP port, not a display
// number.
var Default = []string{"vncviewer", "{host}::{port}"}

// startupWindow is how long after starting a viewer an error exit is
// reported. Later exits are how viewers normally end.
const startupWindow = 5 * time.Second

var (
	placeholderPattern = regexp.MustCompile(`\{[^{}]*\}`)
	knownPlaceholders  = map[string]bool{"{host}": true, "{port}": true, "{title}": true}
)

// Validate checks a viewer command from the config file.
func Validate(argv []string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return errors.New("viewer must start with the program to run")
	}
	if placeholderPattern.MatchString(argv[0]) {
		return fmt.Errorf("viewer: the program name %q can't contain placeholders", argv[0])
	}
	for _, arg := range argv[1:] {
		for _, p := range placeholderPattern.FindAllString(arg, -1) {
			if !knownPlaceholders[p] {
				return fmt.Errorf("viewer: unknown placeholder %s; use {host}, {port} or {title}", p)
			}
		}
	}
	return nil
}

// Target is what a viewer connects to.
type Target struct {
	Host  string
	Port  int
	Title string
}

// Expand replaces the placeholders within each argument. A value never
// becomes extra arguments, and a value that itself contains a placeholder
// is not expanded again.
func Expand(argv []string, t Target) []string {
	r := strings.NewReplacer("{host}", t.Host, "{port}", strconv.Itoa(t.Port), "{title}", t.Title)
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = r.Replace(arg)
	}
	return out
}

// Process is a started viewer.
type Process struct {
	cmd     *exec.Cmd
	name    string
	started time.Time
	stderr  *limitbuf.Buffer
}

// Start runs the viewer for t: directly, never through a shell, in its own
// session, with its output discarded except for error messages.
func Start(argv []string, t Target) (*Process, error) {
	args := Expand(argv, t)
	path, err := platform.FindCommand(args[0])
	if err != nil {
		if args[0] == Default[0] && platform.ViewerInstallHint != "" {
			return nil, fmt.Errorf("%w; %s, or choose another viewer in the config file", err, platform.ViewerInstallHint)
		}
		return nil, err
	}
	cmd := exec.Command(path, args[1:]...)
	stderr := limitbuf.New(2048)
	cmd.Stderr = stderr
	platform.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", args[0], err)
	}
	return &Process{cmd: cmd, name: filepath.Base(args[0]), started: time.Now(), stderr: stderr}, nil
}

// Name is the viewer's program name, for messages.
func (p *Process) Name() string { return p.name }

// Wait waits for the viewer to exit. It returns an error only if the
// viewer failed within its first few seconds, which usually means it
// couldn't start properly.
func (p *Process) Wait() error {
	err := p.cmd.Wait()
	if err == nil || time.Since(p.started) > startupWindow {
		return nil
	}
	if msg := strings.TrimSpace(p.stderr.String()); msg != "" {
		return fmt.Errorf("%s: %s", p.name, msg)
	}
	return fmt.Errorf("%s: %w", p.name, err)
}
