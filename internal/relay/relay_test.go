package relay

import (
	"context"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// catDialer stands in for ssh -W: it echoes everything back.
func catDialer(ctx context.Context) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "cat"), nil
}

func TestTCPListenerRelaysAndClosesWhenIdle(t *testing.T) {
	var mu sync.Mutex
	var changes []int
	ln, err := ListenTCP(catDialer, TCPOptions{
		Idle: 100 * time.Millisecond,
		OnChange: func(n int) {
			mu.Lock()
			changes = append(changes, n)
			mu.Unlock()
		},
		OnError: func(err error) { t.Errorf("relay error: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(ln.Port()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 12)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "RFB 003.008\n" {
		t.Errorf("got %q", buf)
	}
	conn.Close()

	select {
	case <-ln.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not close after the idle period")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 2 || changes[0] != 1 || changes[1] != 0 {
		t.Errorf("got connection counts %v, want [1 0]", changes)
	}
}

func TestTCPListenerClosesIfNobodyConnects(t *testing.T) {
	ln, err := ListenTCP(catDialer, TCPOptions{FirstConnect: 100 * time.Millisecond, Idle: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ln.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("listener stayed open although no viewer connected")
	}
	if !ln.Closed() {
		t.Error("Closed() should report true")
	}
}

func TestTCPListenerStaysOpenWhileConnected(t *testing.T) {
	ln, err := ListenTCP(catDialer, TCPOptions{FirstConnect: 100 * time.Millisecond, Idle: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(ln.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Well past both timeouts, a live connection keeps the listener open.
	time.Sleep(400 * time.Millisecond)
	if ln.Closed() {
		t.Fatal("listener closed while a viewer was connected")
	}
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil || buf[0] != 'x' {
		t.Errorf("relay stopped working: %v %q", err, buf)
	}
}

func TestPipeReportsSSHErrors(t *testing.T) {
	dial := func(ctx context.Context) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'channel 0: open failed: connect failed: Connection refused' >&2; exit 1"), nil
	}
	client, server := net.Pipe()
	defer client.Close()
	err := Pipe(context.Background(), server, dial)
	if err == nil || !strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("got %v", err)
	}
}

func TestPipeStopsWhenContextIsCancelled(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Pipe(ctx, server, catDialer) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled relay should not report an error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Pipe did not return after cancel")
	}
}
