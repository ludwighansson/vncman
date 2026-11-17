// Package ssh runs the system OpenSSH client, sharing one master connection
// per node (ADR 0002).
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrNotConnected means there is no master connection to the node. Commands
// never open a new connection on their own, because ssh might wait silently
// for a passphrase or a security-key touch.
var ErrNotConnected = errors.New("the SSH connection has closed")

// ConnError means ssh itself failed (exit status 255).
type ConnError struct{ Stderr string }

func (e *ConnError) Error() string {
	if e.Stderr == "" {
		return "ssh failed"
	}
	return "ssh: " + e.Stderr
}

// RemoteError means the remote command ran and exited with a non-zero
// status.
type RemoteError struct {
	Code   int
	Stderr string
}

func (e *RemoteError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("remote command exited with status %d", e.Code)
	}
	return e.Stderr
}

// ValidateDestination checks that dest can safely be passed to ssh as a
// destination.
func ValidateDestination(dest string) error {
	switch {
	case strings.TrimSpace(dest) == "":
		return errors.New("node name is empty")
	case strings.HasPrefix(dest, "-"):
		return fmt.Errorf("node name %q must not start with '-'", dest)
	case strings.ContainsAny(dest, " \t\r\n"):
		return fmt.Errorf("node name %q must not contain whitespace", dest)
	}
	return nil
}

// macOS limits UNIX socket paths to 103 bytes. ssh expands %C to 40 hex
// characters and appends a 17-character temporary suffix while it creates
// the socket.
const maxControlDirLen = 103 - 1 - 40 - 17

// ControlDir creates and returns the directory that holds master sockets,
// under the cache directory base.
func ControlDir(base string) (string, error) {
	dir := filepath.Join(base, "cm")
	if len(dir) > maxControlDirLen {
		return "", fmt.Errorf("%s is too long for an ssh control socket path", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Client runs ssh commands against one node.
type Client struct {
	dest        string
	controlPath string
	persist     string
	binary      string
}

// New returns a client for dest. Master sockets live in controlDir.
func New(dest, controlDir, persist string) (*Client, error) {
	if err := ValidateDestination(dest); err != nil {
		return nil, err
	}
	return &Client{
		dest:        dest,
		controlPath: filepath.Join(controlDir, "%C"),
		persist:     persist,
		binary:      "ssh",
	}, nil
}

// Dest returns the node's SSH destination.
func (c *Client) Dest() string { return c.dest }

func (c *Client) args(extra ...string) []string {
	path := c.controlPath
	if strings.ContainsAny(path, " \t") {
		path = `"` + path + `"`
	}
	return append([]string{"-o", "ControlPath=" + path}, extra...)
}

// batchArgs are for every command after the master is up: reuse the master
// and never prompt.
func (c *Client) batchArgs(extra ...string) []string {
	return c.args(append([]string{"-o", "ControlMaster=no", "-o", "BatchMode=yes"}, extra...)...)
}

// Check reports whether a master connection to the node is running.
func (c *Client) Check(ctx context.Context) bool {
	return exec.CommandContext(ctx, c.binary, c.args("-O", "check", c.dest)...).Run() == nil
}

// ConnectCommand returns the command that opens the master connection. Run
// it in the foreground with the terminal attached, so ssh can ask for a
// passphrase, a host-key confirmation or a security-key touch. ssh moves the
// master to the background once the session ends.
func (c *Client) ConnectCommand() *exec.Cmd {
	return exec.Command(c.binary, c.args(
		"-n",
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist="+c.persist,
		c.dest, "true",
	)...)
}

// Run runs a shell command on the node over the master connection. stdin,
// if not nil, is sent to the command's standard input.
func (c *Client) Run(ctx context.Context, remote string, stdin []byte) (stdout, stderr []byte, err error) {
	if !c.Check(ctx) {
		return nil, nil, ErrNotConnected
	}
	cmd := exec.CommandContext(ctx, c.binary, c.batchArgs("-T", c.dest, remote)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), classify(err, errOut.String())
}

func classify(err error, stderr string) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(stderr)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 255 {
			return &ConnError{Stderr: msg}
		}
		return &RemoteError{Code: exitErr.ExitCode(), Stderr: msg}
	}
	return fmt.Errorf("running ssh: %w", err)
}

// ForwardCommand returns a command whose standard input and output are
// connected to host:port as seen from the node (ssh -W).
func (c *Client) ForwardCommand(ctx context.Context, host string, port int) *exec.Cmd {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	return exec.CommandContext(ctx, c.binary, c.batchArgs("-W", target, c.dest)...)
}

// Exit asks the master connection to stop.
func (c *Client) Exit() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, c.binary, c.args("-O", "exit", c.dest)...).Run()
}
