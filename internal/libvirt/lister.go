package libvirt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/ludwighansson/vncman/internal/ssh"
)

// Runner runs a shell command on a node. *ssh.Client implements it.
type Runner interface {
	Run(ctx context.Context, remote string, stdin []byte) (stdout, stderr []byte, err error)
}

// ErrPasswordRequired means sudo on the node needs a password. Call
// SetPassword and try again.
var ErrPasswordRequired = errors.New("sudo on the node needs a password")

// ErrBadPassword means sudo rejected the password given with SetPassword.
var ErrBadPassword = errors.New("sudo rejected the password")

// Error reports why virsh failed at each step that was tried.
type Error struct{ Failures []string }

func (e *Error) Error() string {
	return "virsh failed: " + strings.Join(e.Failures, "; ")
}

// Sudo modes, matching the config file's values.
const (
	SudoAuto   = "auto"
	SudoAlways = "always"
	SudoNever  = "never"
)

// step is one way of running virsh, from least to most privileged.
type step int

const (
	stepPlain        step = iota // virsh
	stepSudo                     // sudo -n virsh
	stepSudoPassword             // sudo -S virsh, password on stdin
)

func (s step) String() string {
	switch s {
	case stepPlain:
		return "without sudo"
	case stepSudo:
		return "with sudo"
	default:
		return "with sudo and a password"
	}
}

// Lister lists the running VMs on one node. It is safe for concurrent use.
type Lister struct {
	runner Runner
	virsh  string
	first  step
	last   step

	mu       sync.Mutex
	password []byte
	resolved step // the step that last worked, or -1
}

// NewLister returns a lister that runs the virsh command over runner, using
// sudo as sudoMode says (SudoAuto, SudoAlways or SudoNever).
func NewLister(runner Runner, virsh, sudoMode string) *Lister {
	l := &Lister{runner: runner, virsh: virsh, first: stepPlain, last: stepSudoPassword, resolved: -1}
	switch sudoMode {
	case SudoNever:
		l.last = stepPlain
	case SudoAlways:
		l.first = stepSudo
	}
	return l
}

// SetPassword sets the sudo password for later commands.
func (l *Lister) SetPassword(password string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.password = []byte(password)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// List returns the running domains on the node, sorted by display name.
func (l *Lister) List(ctx context.Context) ([]Domain, error) {
	out, err := l.run(ctx, "list --uuid --state-running")
	if err != nil {
		return nil, err
	}
	uuids, err := parseUUIDs(out)
	if err != nil {
		return nil, err
	}
	if len(uuids) == 0 {
		return nil, nil
	}
	return l.dump(ctx, uuids)
}

// Lookup returns the current definition of one domain.
func (l *Lister) Lookup(ctx context.Context, uuid string) (Domain, error) {
	if !uuidPattern.MatchString(uuid) {
		return Domain{}, fmt.Errorf("invalid domain UUID %q", uuid)
	}
	domains, err := l.dump(ctx, []string{uuid})
	if err != nil {
		return Domain{}, err
	}
	if len(domains) != 1 {
		return Domain{}, fmt.Errorf("domain %s was not found", uuid)
	}
	return domains[0], nil
}

// dump prints the XML of every domain with a single virsh invocation.
func (l *Lister) dump(ctx context.Context, uuids []string) ([]Domain, error) {
	cmds := make([]string, len(uuids))
	for i, u := range uuids {
		cmds[i] = "dumpxml " + u
	}
	out, err := l.run(ctx, shellQuote(strings.Join(cmds, "; ")))
	if err != nil && len(out) == 0 {
		return nil, err
	}
	// If a domain stopped between "list" and "dumpxml", virsh reports an
	// error for it but still prints the others.
	domains, perr := ParseDomains(bytes.NewReader(out))
	if perr != nil {
		return nil, perr
	}
	SortDomains(domains)
	return domains, nil
}

// run runs "virsh <args>" on the node, starting at the step that last worked
// and escalating to sudo when the mode allows it. On a partial failure it
// returns both the output and the error.
func (l *Lister) run(ctx context.Context, args string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	start := l.first
	if l.resolved >= 0 {
		start = l.resolved
	}
	var failures []string
	for s := start; s <= l.last; s++ {
		if s == stepSudoPassword && l.password == nil {
			return nil, ErrPasswordRequired
		}
		out, errOut, err := l.runner.Run(ctx, l.command(s, args), l.stdin(s))
		if err == nil || len(out) > 0 {
			l.resolved = s
			return out, err
		}
		var remote *ssh.RemoteError
		if !errors.As(err, &remote) {
			// ssh itself failed, so trying sudo would not help.
			return nil, err
		}
		msg := oneLine(string(errOut))
		if msg == "" {
			msg = err.Error()
		}
		switch {
		case s == stepSudo && wantsPassword(msg):
			continue
		case s == stepSudoPassword && rejectedPassword(msg):
			l.password = nil
			return nil, ErrBadPassword
		}
		failures = append(failures, s.String()+": "+msg)
		if s >= stepSudo {
			// sudo refused for a reason a password won't fix.
			break
		}
	}
	return nil, &Error{Failures: failures}
}

func (l *Lister) command(s step, args string) string {
	switch s {
	case stepPlain:
		return l.virsh + " " + args
	case stepSudo:
		return "sudo -n " + l.virsh + " " + args
	default:
		return "sudo -S -p '' " + l.virsh + " " + args
	}
}

func (l *Lister) stdin(s step) []byte {
	if s != stepSudoPassword {
		return nil
	}
	return append(append([]byte{}, l.password...), '\n')
}

func wantsPassword(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "password is required") ||
		strings.Contains(msg, "a terminal is required") ||
		strings.Contains(msg, "no tty present")
}

func rejectedPassword(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "incorrect password") ||
		strings.Contains(msg, "sorry, try again") ||
		strings.Contains(msg, "no password was provided")
}

func parseUUIDs(out []byte) ([]string, error) {
	var uuids []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !uuidPattern.MatchString(line) {
			return nil, fmt.Errorf("unexpected line in virsh list output: %q", line)
		}
		uuids = append(uuids, strings.ToLower(line))
	}
	return uuids, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func oneLine(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " / ")
}
