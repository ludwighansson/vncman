package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ludwighansson/vncman/internal/config"
	"github.com/ludwighansson/vncman/internal/libvirt"
)

var plainDomains = []libvirt.Domain{
	{Name: "storage01.example.com", UUID: "7600a15c-804d-450e-a088-eb62a0f7f96e", VNC: &libvirt.VNC{Host: "127.0.0.1", Port: 5900}},
	{Name: "web01", UUID: "610964c9-5469-4c62-8737-233c94242fae", VNC: &libvirt.VNC{Host: "127.0.0.1", Port: 5903}},
	{Name: "worker01.example.com", UUID: "a5b13a45-9589-4543-844d-b003296c780d", Graphics: "spice"},
}

var novaDomain = libvirt.Domain{
	Name: "instance-0000a1b2", UUID: "45bd23e4-39fd-4c00-a5f7-edf0d3ed2ae1",
	Nova: &libvirt.Nova{Name: "web-prod-1", Project: "acme-prod", Flavor: "m1.large"},
	VNC:  &libvirt.VNC{Host: "127.0.0.1", Port: 5901},
}

func newModel(t *testing.T, recent ...string) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.Nodes = []config.Node{{Name: "compute-01", Group: "region-a"}, {Name: "node1"}}
	m := New(Options{Config: cfg, State: &config.State{Recent: recent}, StatePath: t.TempDir() + "/state.json"})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

func titles(cols []column) string {
	var t []string
	for _, c := range cols {
		t = append(t, c.title)
	}
	return strings.Join(t, ",")
}

func TestColumnsFollowTheData(t *testing.T) {
	m := newModel(t)
	m.current = &node{name: "node1"}
	m.Update(listedMsg{node: "node1", domains: plainDomains})
	if got := titles(m.cols); got != " ,Name,UUID,VNC" {
		t.Errorf("plain libvirt node: got columns %s", got)
	}

	m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	m.Update(listedMsg{node: "node1", domains: append([]libvirt.Domain{novaDomain}, plainDomains...)})
	if got := titles(m.cols); got != " ,Name,Project,Flavor,Domain,UUID,VNC" {
		t.Errorf("node with Nova instances: got columns %s", got)
	}
	for _, c := range m.cols {
		if c.title == "Domain" && c.width != len("instance-0000a1b2") {
			t.Errorf("Domain column should only hold names that differ from the display name; width %d", c.width)
		}
	}

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if got := titles(m.cols); strings.Contains(got, "UUID") {
		t.Errorf("narrow terminal should drop UUID, got %s", got)
	}
	total := 0
	for _, c := range m.cols {
		total += c.width + 2
	}
	if total > 80 {
		t.Errorf("columns are %d wide in an 80-column terminal", total)
	}
}

func TestFilter(t *testing.T) {
	all := append([]libvirt.Domain{novaDomain}, plainDomains...)
	for query, want := range map[string]int{
		"":                   4,
		"web":                2,
		"WEB prod":           1,
		"acme":               1,
		"610964c9":           1, // UUID from a ticket
		"example.com":        2,
		"nothing-like-this":  0,
		"instance-0000a1b2 ": 1,
	} {
		if got := len(filterDomains(all, query)); got != want {
			t.Errorf("filter %q: got %d, want %d", query, got, want)
		}
	}
}

func TestListFlow(t *testing.T) {
	m := newModel(t)
	m.current = &node{name: "node1"}
	m.mode = modeLoading

	m.Update(listedMsg{node: "node1", err: libvirt.ErrPasswordRequired})
	if m.mode != modePassword {
		t.Fatalf("password required: got mode %d", m.mode)
	}
	m.Update(listedMsg{node: "node1", err: libvirt.ErrBadPassword})
	if m.mode != modePassword || m.passwordMsg == "" {
		t.Fatalf("bad password: got mode %d, message %q", m.mode, m.passwordMsg)
	}

	m.Update(listedMsg{node: "node1", domains: plainDomains})
	if m.mode != modeList || len(m.table.Rows()) != 3 {
		t.Fatalf("got mode %d with %d rows", m.mode, len(m.table.Rows()))
	}
	// Results for a node we have left are ignored.
	m.Update(listedMsg{node: "other", domains: nil})
	if len(m.table.Rows()) != 3 {
		t.Error("a stale result replaced the list")
	}

	// A VM without VNC over TCP can't be opened.
	m.table.SetCursor(2)
	if cmd := m.open(config.ConsoleBrowser); cmd != nil || !m.statusErr || !strings.Contains(m.status, "spice") {
		t.Errorf("opening a SPICE VM: got status %q", m.status)
	}

	// A browser connects to a console: the VM is marked, and quitting asks first.
	c := &vmConsole{key: "node1/" + plainDomains[1].UUID, token: "tok"}
	m.consoles[c.key], m.byToken["tok"] = c, c
	m.Update(webActiveMsg{token: "tok", active: 1})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.mode != modeConfirmQuit {
		t.Fatalf("q with an open console: got mode %d", m.mode)
	}
	if !strings.Contains(m.View(), "1 console is open") {
		t.Errorf("confirmation not shown:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.mode != modeList {
		t.Errorf("answering n: got mode %d", m.mode)
	}
	if !strings.Contains(m.table.View(), "●") {
		t.Error("open console is not marked in the table")
	}
}

func TestPastedFilter(t *testing.T) {
	m := newModel(t)
	m.current = &node{name: "node1"}
	m.Update(listedMsg{node: "node1", domains: plainDomains})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/web01")})
	if !m.filtering || m.filter.Value() != "web01" || len(m.visible) != 1 {
		t.Errorf("got filtering=%v value=%q visible=%d", m.filtering, m.filter.Value(), len(m.visible))
	}
}

func TestPickChoices(t *testing.T) {
	m := newModel(t, "node1", "node9")
	var names []string
	for _, c := range m.pickChoices() {
		names = append(names, c.name+"("+c.note+")")
	}
	if got := strings.Join(names, " "); got != "node1(recent) node9(recent) compute-01(region-a)" {
		t.Errorf("got %s", got)
	}

	m.pick.SetValue("comp")
	choices := m.pickChoices()
	if len(choices) != 2 || choices[0].name != "comp" || choices[1].name != "compute-01" {
		t.Errorf("typed text should come first, then matches: %+v", choices)
	}
	m.pick.SetValue("node1")
	if choices := m.pickChoices(); len(choices) != 1 || choices[0].name != "node1" {
		t.Errorf("exact match should not be duplicated: %+v", choices)
	}
}

// A viewer that hands the connection to a running instance exits at once.
// The relay must stay open, and opening the VM again reuses it (ADR 0009).
func TestViewerHandOff(t *testing.T) {
	m := newModel(t)
	m.opts.Config.Viewer = config.Argv{"true"}
	m.current = &node{name: "node1"}
	m.Update(listedMsg{node: "node1", domains: plainDomains})
	m.table.SetCursor(1)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	c := m.consoles["node1/"+plainDomains[1].UUID]
	if c == nil || c.relay == nil || c.relay.Closed() {
		t.Fatalf("relay is not open: %+v (status %q)", c, m.status)
	}
	defer c.relay.Close()
	if !strings.Contains(m.status, "Opened web01 in true") {
		t.Errorf("status: %q", m.status)
	}

	port := c.relay.Port()
	time.Sleep(200 * time.Millisecond) // the viewer has exited by now
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if c.relay.Closed() || c.relay.Port() != port {
		t.Error("opening the VM again should reuse the open relay")
	}
	if !strings.Contains(m.help(), "v viewer") {
		t.Errorf("help: %q", m.help())
	}
}

// If the last console closes while VNCMan asks whether to quit, it quits.
func TestQuitWhenLastConsoleClosesDuringConfirmation(t *testing.T) {
	m := newModel(t)
	m.current = &node{name: "node1"}
	m.Update(listedMsg{node: "node1", domains: plainDomains})
	c := &vmConsole{key: "node1/" + plainDomains[1].UUID, token: "tok"}
	m.consoles[c.key], m.byToken["tok"] = c, c
	m.Update(webActiveMsg{token: "tok", active: 1})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.mode != modeConfirmQuit {
		t.Fatalf("got mode %d", m.mode)
	}
	_, cmd := m.Update(webActiveMsg{token: "tok", active: 0})
	if cmd == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("expected tea.Quit")
	}
}
