//go:build unix

package platform

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/ludwighansson/vncman/internal/limitbuf"
)

// Detach starts cmd in its own session, so it isn't tied to VNCMan's
// terminal and keeps running after VNCMan exits.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// launchWait is how long launch waits for a hand-off program to finish.
const launchWait = 5 * time.Second

// launch starts a program that hands off to another one, such as open or
// xdg-open. It reports a failure if the program exits with an error within
// a few seconds. If it is still running after that, it is left to run on
// its own: xdg-open can wait for the browser it started.
func launch(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	stderr := limitbuf.New(2048)
	cmd.Stderr = stderr
	Detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %s", name, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	case <-time.After(launchWait):
		return nil
	}
}
