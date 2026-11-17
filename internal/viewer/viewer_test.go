package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, ok := range [][]string{
		Default,
		{"remmina", "-c", "vnc://{host}:{port}"},
		{"remote-viewer", "--title", "{title}", "vnc://{host}:{port}"},
		{"open", "vnc://{host}:{port}"},
	} {
		if err := Validate(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range [][]string{
		nil,
		{""},
		{"{title}", "x"},
		{"remmina", "vnc://{hostname}:{port}"},
	} {
		if err := Validate(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestExpandKeepsValuesInOneArgument(t *testing.T) {
	title := `web 01; rm -rf ~ "$(reboot)" {port} · node1`
	got := Expand([]string{"viewer", "--title", "{title}", "{host}::{port}"}, Target{Host: "127.0.0.1", Port: 51234, Title: title})
	want := []string{"viewer", "--title", title, "127.0.0.1::51234"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The viewer gets exactly the expanded arguments, with no shell between.
func TestStartPassesArgumentsVerbatim(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args")
	title := `it's "web01" · node1; echo injected`
	// The test itself uses sh to record its arguments; VNCMan adds no shell.
	argv := []string{"sh", "-c", `printf '%s\n' "$@" > "$0"`, out, "{host}", "{port}", "{title}"}
	p, err := Start(argv, Target{Host: "127.0.0.1", Port: 5901, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "sh" {
		t.Errorf("name: got %q", p.Name())
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "127.0.0.1\n5901\n"+title+"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEarlyFailureIsReported(t *testing.T) {
	p, err := Start([]string{"sh", "-c", "echo 'could not connect' >&2; exit 3"}, Target{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err == nil || !strings.Contains(err.Error(), "could not connect") {
		t.Errorf("got %v", err)
	}
}

func TestMissingProgram(t *testing.T) {
	if _, err := Start([]string{"vncman-no-such-viewer"}, Target{}); err == nil {
		t.Error("expected an error")
	}
}
