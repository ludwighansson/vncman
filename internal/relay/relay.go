// Package relay copies VNC traffic between local clients and ssh -W
// channels to the node (ADR 0003).
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ludwighansson/vncman/internal/limitbuf"
)

// Dialer starts a command whose standard input and output reach the VNC
// server, typically ssh -W. The command must be created with
// exec.CommandContext(ctx, ...), so cancelling ctx stops it.
type Dialer func(ctx context.Context) (*exec.Cmd, error)

// exitGrace is how long ssh gets to exit after one side has closed.
const exitGrace = 2 * time.Second

// Pipe connects conn to a command from dial and copies bytes both ways
// until either side closes. It always closes conn.
func Pipe(ctx context.Context, conn io.ReadWriteCloser, dial Dialer) error {
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Errors after the relay was cancelled are expected, not failures.
	cancelled := func(err error) error {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	cmd, err := dial(ctx)
	if err != nil {
		return cancelled(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := limitbuf.New(4096)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return cancelled(fmt.Errorf("starting ssh: %w", err))
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stdin, conn)
		_ = stdin.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, stdout)
		done <- struct{}{}
	}()

	// When one direction ends, close both ends so the other one ends too.
	<-done
	_ = conn.Close()
	_ = stdin.Close()

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-time.After(exitGrace):
		cancel()
		waitErr = <-waited
	}
	<-done

	if waitErr == nil || ctx.Err() != nil {
		return nil
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return errors.New(msg)
	}
	return fmt.Errorf("ssh: %w", waitErr)
}

// TCPOptions control a TCPListener.
type TCPOptions struct {
	// FirstConnect closes the listener if no client connects this soon
	// after it opens. Zero means no limit.
	FirstConnect time.Duration
	// Idle closes the listener this long after its last connection ends.
	Idle time.Duration
	// OnChange, if set, is called with the number of active connections
	// whenever it changes.
	OnChange func(active int)
	// OnError, if set, is called with each relay failure.
	OnError func(error)
}

// TCPListener accepts VNC clients on a loopback port and relays each
// connection. It serves native viewers. It closes on inactivity rather
// than when a viewer process exits, because some viewers hand off to a
// running instance and exit at once (ADR 0009).
type TCPListener struct {
	ln   net.Listener
	dial Dialer
	opts TCPOptions

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	active int
	timer  *time.Timer
	closed bool
}

// ListenTCP listens on 127.0.0.1 on a port the OS picks.
func ListenTCP(dial Dialer, opts TCPOptions) (*TCPListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &TCPListener{ln: ln, dial: dial, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if opts.FirstConnect > 0 {
		l.mu.Lock()
		l.timer = time.AfterFunc(opts.FirstConnect, l.Close)
		l.mu.Unlock()
	}
	go l.serve()
	return l, nil
}

// Port returns the loopback port the listener accepts connections on.
func (l *TCPListener) Port() int { return l.ln.Addr().(*net.TCPAddr).Port }

// Done is closed when the listener has closed.
func (l *TCPListener) Done() <-chan struct{} { return l.done }

// Closed reports whether the listener has closed.
func (l *TCPListener) Closed() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

// Close stops accepting connections and ends the active ones.
func (l *TCPListener) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	if l.timer != nil {
		l.timer.Stop()
	}
	l.mu.Unlock()

	l.cancel()
	_ = l.ln.Close()
	close(l.done)
}

func (l *TCPListener) serve() {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			l.Close()
			return
		}
		l.adjust(+1)
		go func() {
			defer l.adjust(-1)
			if err := Pipe(l.ctx, conn, l.dial); err != nil && l.opts.OnError != nil {
				l.opts.OnError(err)
			}
		}()
	}
}

func (l *TCPListener) adjust(delta int) {
	l.mu.Lock()
	l.active += delta
	active := l.active
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	if active == 0 && delta < 0 && !l.closed {
		l.timer = time.AfterFunc(l.opts.Idle, l.Close)
	}
	l.mu.Unlock()
	if l.opts.OnChange != nil {
		l.opts.OnChange(active)
	}
}
