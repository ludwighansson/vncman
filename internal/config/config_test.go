package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Viewer) != 2 || cfg.Viewer[0] != "vncviewer" || cfg.Viewer[1] != "{host}::{port}" {
		t.Errorf("default viewer: got %q", cfg.Viewer)
	}
	if cfg.Console != ConsoleBrowser || cfg.Sudo != SudoAuto || cfg.Virsh != DefaultVirsh || cfg.SSH.ControlPersist != "10m" {
		t.Errorf("got %+v", cfg)
	}
}

func TestLoadExample(t *testing.T) {
	cfg, err := Load(write(t, `
console = "viewer"
viewer = ["remote-viewer", "--title", "{title}", "vnc://{host}:{port}"]
http_port = 7420
sudo = "auto"
virsh = "virsh -r -c qemu:///system"

[ssh]
control_persist = "30m"

[[nodes]]
name = "compute-01"
group = "region-a"

[[nodes]]
name = "kolla-compute-07"
virsh = "docker exec nova_libvirt virsh -r"

[[nodes]]
name = "compute-12"
sudo = "always"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Console != ConsoleViewer || len(cfg.Viewer) != 4 || cfg.Viewer[2] != "{title}" || cfg.HTTPPort != 7420 || cfg.SSH.ControlPersist != "30m" {
		t.Errorf("got %+v", cfg)
	}
	if s := cfg.ForNode("compute-01"); s.Sudo != SudoAuto || s.Virsh != DefaultVirsh {
		t.Errorf("compute-01: got %+v", s)
	}
	if s := cfg.ForNode("kolla-compute-07"); s.Virsh != "docker exec nova_libvirt virsh -r" || s.Sudo != SudoAuto {
		t.Errorf("kolla-compute-07: got %+v", s)
	}
	if s := cfg.ForNode("compute-12"); s.Sudo != SudoAlways {
		t.Errorf("compute-12: got %+v", s)
	}
	if s := cfg.ForNode("unlisted"); s.Sudo != SudoAuto || s.Virsh != DefaultVirsh {
		t.Errorf("unlisted node: got %+v", s)
	}
	if g := cfg.Group("compute-01"); g != "region-a" {
		t.Errorf("group: got %q", g)
	}
}

func TestInvalidConfig(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown key":      `colour = "blue"`,
		"bad console":      `console = "vnc"`,
		"old console":      `console = "tigervnc"`,
		"viewer typo":      `viewer = ["remmina", "vnc://{hots}:{port}"]`,
		"empty viewer":     `viewer = []`,
		"viewer number":    `viewer = ["remmina", 5900]`,
		"bad sudo":         `sudo = true`,
		"bad node sudo":    "[[nodes]]\nname = \"n1\"\nsudo = \"sometimes\"",
		"option as a node": "[[nodes]]\nname = \"-oProxyCommand=evil\"",
		"duplicate node":   "[[nodes]]\nname = \"n1\"\n[[nodes]]\nname = \"n1\"",
		"empty virsh":      `virsh = ""`,
		"port range":       `http_port = 70000`,
	} {
		if _, err := Load(write(t, contents)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestViewerMustBeAList(t *testing.T) {
	_, err := Load(write(t, `viewer = "remmina -c vnc://{host}:{port}"`))
	if err == nil || !strings.Contains(err.Error(), "must be a list of arguments") {
		t.Errorf("got %v", err)
	}
}

func TestState(t *testing.T) {
	var s State
	for _, n := range []string{"a", "b", "c", "a"} {
		s.AddRecent(n)
	}
	if got := strings.Join(s.Recent, ","); got != "a,c,b" {
		t.Errorf("recent: got %s", got)
	}
	for i := 0; i < 20; i++ {
		s.AddRecent(string(rune('d' + i)))
	}
	if len(s.Recent) != maxRecent {
		t.Errorf("recent should be capped at %d, got %d", maxRecent, len(s.Recent))
	}

	path := filepath.Join(t.TempDir(), "sub", "state.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.Recent, ",") != strings.Join(s.Recent, ",") {
		t.Errorf("round trip: got %v, want %v", loaded.Recent, s.Recent)
	}
}
