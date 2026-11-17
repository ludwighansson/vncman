// Package tui is VNCMan's terminal UI (ADR 0005).
package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ludwighansson/vncman/internal/config"
	"github.com/ludwighansson/vncman/internal/libvirt"
	"github.com/ludwighansson/vncman/internal/platform"
	"github.com/ludwighansson/vncman/internal/relay"
	"github.com/ludwighansson/vncman/internal/ssh"
	"github.com/ludwighansson/vncman/internal/viewer"
	"github.com/ludwighansson/vncman/internal/web"
)

// A viewer's relay closes if no viewer connects within viewerFirstConnect,
// or viewerIdle after its last connection ends (ADR 0009).
const (
	viewerFirstConnect = 60 * time.Second
	viewerIdle         = 30 * time.Second
)

// Options are what the UI needs from main.
type Options struct {
	Config     config.Config
	State      *config.State
	StatePath  string
	ControlDir string
	Web        *web.Server
}

type mode int

const (
	modePick        mode = iota // choosing a node
	modeLoading                 // connecting or listing
	modePassword                // asking for the sudo password
	modeList                    // browsing VMs
	modeConfirmQuit             // consoles are open; confirm before quitting
)

type node struct {
	name   string
	ssh    *ssh.Client
	lister *libvirt.Lister
}

// vmConsole tracks the consoles open for one VM.
type vmConsole struct {
	key          string
	title        string
	token        string // browser console token, once created
	webActive    int
	relay        *relay.TCPListener // the viewer's relay, while it is open
	viewerActive int
}

// open reports whether a browser or viewer is connected to the VM. VNCMan
// can't tell when a viewer window closes, only when its connection does.
func (c *vmConsole) open() bool {
	return c.webActive > 0 || c.viewerActive > 0
}

// Model is the Bubble Tea model.
type Model struct {
	opts Options
	send func(tea.Msg)

	width, height int
	mode          mode
	back          mode // mode to return to when quitting is cancelled

	nodes   map[string]*node
	current *node

	pick       textinput.Model
	pickCursor int

	domains   []libvirt.Domain
	visible   []libvirt.Domain
	cols      []column
	table     table.Model
	filter    textinput.Model
	filtering bool

	password    textinput.Model
	passwordMsg string

	spinner spinner.Model
	loading string

	status    string
	statusErr bool

	consoles map[string]*vmConsole // by node + "/" + UUID
	byToken  map[string]*vmConsole
}

// Messages from commands and from console goroutines.
type (
	masterMsg struct {
		node  string
		alive bool
	}
	connectedMsg struct {
		node string
		err  error
	}
	listedMsg struct {
		node    string
		domains []libvirt.Domain
		err     error
	}
	webActiveMsg struct {
		token  string
		active int
	}
	webErrorMsg struct {
		token string
		err   error
	}
	viewerActiveMsg struct {
		key    string
		active int
	}
	viewerErrorMsg struct {
		key string
		err error
	}
	relayClosedMsg struct {
		key   string
		relay *relay.TCPListener
	}
	statusMsg struct {
		text string
		err  bool
	}
)

// New returns the UI. It starts in the node picker unless Connect is
// called before the program runs.
func New(opts Options) *Model {
	m := &Model{
		opts:     opts,
		send:     func(tea.Msg) {},
		mode:     modePick,
		nodes:    make(map[string]*node),
		consoles: make(map[string]*vmConsole),
		byToken:  make(map[string]*vmConsole),
	}

	m.pick = textinput.New()
	m.pick.Prompt = "Node: "
	m.pick.Placeholder = "host name or ssh alias"
	m.pick.Focus()

	m.filter = textinput.New()
	m.filter.Prompt = "/"
	m.filter.Placeholder = "filter by name, project, flavor or UUID"

	m.password = textinput.New()
	m.password.Prompt = "Password: "
	m.password.EchoMode = textinput.EchoPassword
	m.password.EchoCharacter = '•'

	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot))

	keys := table.DefaultKeyMap()
	keys.PageUp = key.NewBinding(key.WithKeys("pgup")) // "b" opens the browser console
	m.table = table.New(table.WithFocused(true), table.WithKeyMap(keys), table.WithStyles(tableStyles()))
	return m
}

// SetSend connects the UI to the running program, so console events from
// other goroutines reach it.
func (m *Model) SetSend(send func(tea.Msg)) {
	m.send = send
	m.opts.Web.SetCallbacks(
		func(token string, active int) { send(webActiveMsg{token: token, active: active}) },
		func(token string, err error) { send(webErrorMsg{token: token, err: err}) },
	)
}

// Connect opens the SSH connection to a node before the UI starts, so ssh
// can prompt on the plain terminal. The UI then starts on that node.
func (m *Model) Connect(name string, stdout, stderr io.Writer) error {
	n, err := m.getNode(name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	alive := n.ssh.Check(ctx)
	cancel()
	if !alive {
		cmd := n.ssh.ConnectCommand()
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("could not connect to %s: %w", name, err)
		}
	}
	m.enter(n)
	m.loading = "Listing VMs on " + n.name
	return nil
}

// Close ends every console and SSH connection, and saves the state. Call it
// after the program has exited. Viewer programs are left running: one may
// be a shared instance, such as Remmina, that holds other connections.
func (m *Model) Close() {
	for _, c := range m.consoles {
		if c.relay != nil {
			c.relay.Close()
		}
	}
	m.opts.Web.Close()
	var wg sync.WaitGroup
	for _, n := range m.nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.ssh.Exit()
		}()
	}
	wg.Wait()
	_ = m.opts.State.Save(m.opts.StatePath)
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	if m.current != nil {
		return tea.Batch(m.spinner.Tick, m.listCmd(m.current))
	}
	return textinput.Blink
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutTable()
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, m.quit()
		}
		switch m.mode {
		case modePick:
			return m.updatePick(msg)
		case modePassword:
			return m.updatePassword(msg)
		case modeList:
			return m.updateList(msg)
		case modeConfirmQuit:
			return m.updateConfirm(msg)
		case modeLoading:
			if msg.String() == "q" {
				return m, m.quit()
			}
		}
		return m, nil

	case spinner.TickMsg:
		if m.mode != modeLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case masterMsg:
		n := m.nodes[msg.node]
		if n == nil || n != m.current {
			return m, nil
		}
		if msg.alive {
			m.loading = "Listing VMs on " + n.name
			return m, m.listCmd(n)
		}
		connect := &connectCommand{cmd: n.ssh.ConnectCommand(), node: n.name}
		return m, tea.Exec(connect, func(err error) tea.Msg { return connectedMsg{node: n.name, err: err} })

	case connectedMsg:
		if m.current == nil || msg.node != m.current.name {
			return m, nil
		}
		if msg.err != nil {
			m.setMode(modeList)
			m.setStatus(fmt.Sprintf("Could not connect to %s. Press r to try again, or n to pick another node.", msg.node), true)
			return m, nil
		}
		m.loading = "Listing VMs on " + msg.node
		return m, tea.Batch(m.spinner.Tick, m.listCmd(m.current))

	case listedMsg:
		if m.current == nil || msg.node != m.current.name {
			return m, nil
		}
		switch {
		case errors.Is(msg.err, libvirt.ErrPasswordRequired):
			return m, m.askPassword("")
		case errors.Is(msg.err, libvirt.ErrBadPassword):
			return m, m.askPassword("Sorry, try again.")
		case msg.err != nil:
			m.setMode(modeList)
			m.setStatus(describe(msg.err, msg.node), true)
		default:
			m.setMode(modeList)
			m.domains = msg.domains
			m.applyFilter()
			if len(m.domains) == 0 {
				m.setStatus("No running VMs on "+msg.node+".", false)
			} else {
				m.setStatus("", false)
			}
		}
		return m, nil

	case webActiveMsg:
		if c := m.byToken[msg.token]; c != nil {
			c.webActive = msg.active
		}
		return m, m.consolesChanged()

	case webErrorMsg:
		m.setStatus(m.consoleTitle(m.byToken[msg.token])+": "+msg.err.Error(), true)
		return m, nil

	case viewerActiveMsg:
		if c := m.consoles[msg.key]; c != nil {
			c.viewerActive = msg.active
		}
		return m, m.consolesChanged()

	case viewerErrorMsg:
		m.setStatus(m.consoleTitle(m.consoles[msg.key])+": "+msg.err.Error(), true)
		return m, nil

	case relayClosedMsg:
		if c := m.consoles[msg.key]; c != nil && c.relay == msg.relay {
			c.relay, c.viewerActive = nil, 0
		}
		return m, m.consolesChanged()

	case statusMsg:
		m.setStatus(msg.text, msg.err)
		return m, nil
	}

	// Anything else, such as the cursor blink, goes to the focused input.
	var cmd tea.Cmd
	switch {
	case m.mode == modePick:
		m.pick, cmd = m.pick.Update(msg)
	case m.mode == modePassword:
		m.password, cmd = m.password.Update(msg)
	case m.filtering:
		m.filter, cmd = m.filter.Update(msg)
	}
	return m, cmd
}

func (m *Model) updatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.pickChoices()
	switch msg.String() {
	case "esc":
		if m.current != nil {
			m.pick.Blur()
			m.mode = modeList
			return m, nil
		}
		return m, m.quit()
	case "up", "ctrl+p":
		if m.pickCursor > 0 {
			m.pickCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.pickCursor < len(choices)-1 {
			m.pickCursor++
		}
		return m, nil
	case "enter":
		if len(choices) == 0 {
			return m, nil
		}
		n, err := m.getNode(choices[min(m.pickCursor, len(choices)-1)].name)
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		m.pick.SetValue("")
		m.pick.Blur()
		m.enter(n)
		return m, tea.Batch(m.spinner.Tick, m.checkCmd(n))
	}
	var cmd tea.Cmd
	m.pick, cmd = m.pick.Update(msg)
	m.pickCursor = 0
	return m, cmd
}

func (m *Model) updatePassword(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.current.lister.SetPassword(m.password.Value())
		m.password.SetValue("")
		m.password.Blur()
		m.mode = modeLoading
		m.loading = "Listing VMs on " + m.current.name
		return m, tea.Batch(m.spinner.Tick, m.listCmd(m.current))
	case "esc":
		m.password.SetValue("")
		m.password.Blur()
		m.mode = modeList
		m.setStatus(fmt.Sprintf("Listing VMs on %s needs the sudo password. Press r to try again.", m.current.name), true)
		return m, nil
	}
	var cmd tea.Cmd
	m.password, cmd = m.password.Update(msg)
	return m, cmd
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.applyFilter()
			return m, nil
		case "up", "down", "pgup", "pgdown":
			var cmd tea.Cmd
			m.table, cmd = m.table.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		return m, cmd
	}

	// Characters that arrive together, such as a paste of "/web", come as
	// one key message.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && msg.Runes[0] == '/' {
		m.filtering = true
		m.filter.SetValue(string(msg.Runes[1:]))
		m.filter.CursorEnd()
		m.applyFilter()
		return m, m.filter.Focus()
	}

	switch msg.String() {
	case "q":
		return m, m.quit()
	case "/":
		m.filtering = true
		return m, m.filter.Focus()
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.applyFilter()
		}
		return m, nil
	case "r":
		if m.current == nil {
			return m, nil
		}
		m.mode = modeLoading
		m.loading = "Refreshing " + m.current.name
		return m, tea.Batch(m.spinner.Tick, m.checkCmd(m.current))
	case "n":
		m.mode = modePick
		m.pick.SetValue("")
		m.pickCursor = 0
		m.setStatus("", false)
		return m, m.pick.Focus()
	case "enter":
		return m, m.open(m.opts.Config.Console)
	case "b":
		return m, m.open(config.ConsoleBrowser)
	case "v":
		return m, m.open(config.ConsoleViewer)
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m, tea.Quit
	case "n", "N", "esc":
		m.mode = m.back
	}
	return m, nil
}

// quit asks for confirmation first if any console is open.
func (m *Model) quit() tea.Cmd {
	if m.mode != modeConfirmQuit && m.openConsoles() > 0 {
		m.back = m.mode
		m.mode = modeConfirmQuit
		return nil
	}
	return tea.Quit
}

// consolesChanged redraws the open-console markers. If the last console
// closed while VNCMan was asking whether to quit, it quits: the consoles
// were the only reason to ask.
func (m *Model) consolesChanged() tea.Cmd {
	m.layoutTable()
	if m.mode == modeConfirmQuit && m.openConsoles() == 0 {
		return tea.Quit
	}
	return nil
}

// setMode changes the mode, or the mode to return to while the quit
// confirmation is showing.
func (m *Model) setMode(md mode) {
	if m.mode == modeConfirmQuit {
		m.back = md
		return
	}
	m.mode = md
}

func (m *Model) setStatus(text string, isErr bool) {
	m.status, m.statusErr = text, isErr
	m.layoutTable()
}

func (m *Model) askPassword(msg string) tea.Cmd {
	m.setMode(modePassword)
	m.passwordMsg = msg
	m.password.SetValue("")
	return m.password.Focus()
}

func (m *Model) openConsoles() int {
	n := 0
	for _, c := range m.consoles {
		if c.open() {
			n++
		}
	}
	return n
}

func (m *Model) consoleTitle(c *vmConsole) string {
	if c == nil {
		return "Console"
	}
	return c.title
}

func (m *Model) getNode(name string) (*node, error) {
	name = strings.TrimSpace(name)
	if n := m.nodes[name]; n != nil {
		return n, nil
	}
	client, err := ssh.New(name, m.opts.ControlDir, m.opts.Config.SSH.ControlPersist)
	if err != nil {
		return nil, err
	}
	s := m.opts.Config.ForNode(name)
	n := &node{name: name, ssh: client, lister: libvirt.NewLister(client, s.Virsh, s.Sudo)}
	m.nodes[name] = n
	return n, nil
}

// enter makes n the current node and shows the loading screen.
func (m *Model) enter(n *node) {
	if m.current != n {
		m.domains, m.visible = nil, nil
		m.filtering = false
		m.filter.Blur()
		m.filter.SetValue("")
		m.table.SetCursor(0)
	}
	m.current = n
	m.mode = modeLoading
	m.loading = "Connecting to " + n.name
	m.status = ""
	m.opts.State.AddRecent(n.name)
	if err := m.opts.State.Save(m.opts.StatePath); err != nil {
		m.status, m.statusErr = "Could not save recent nodes: "+err.Error(), true
	}
	m.layoutTable()
}

func (m *Model) checkCmd(n *node) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return masterMsg{node: n.name, alive: n.ssh.Check(ctx)}
	}
}

func (m *Model) listCmd(n *node) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		domains, err := n.lister.List(ctx)
		return listedMsg{node: n.name, domains: domains, err: err}
	}
}

func (m *Model) selected() (libvirt.Domain, bool) {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.visible) {
		return libvirt.Domain{}, false
	}
	return m.visible[i], true
}

// open opens the selected VM's console in the browser or in the viewer.
func (m *Model) open(kind string) tea.Cmd {
	d, ok := m.selected()
	if !ok || m.current == nil {
		return nil
	}
	if d.VNC == nil {
		m.setStatus(fmt.Sprintf("%s has no VNC console reachable over TCP (%s).", d.DisplayName(), d.Graphics), true)
		return nil
	}
	if kind == config.ConsoleViewer {
		return m.openViewer(d)
	}
	return m.openBrowser(d)
}

func (m *Model) consoleFor(d libvirt.Domain) *vmConsole {
	k := m.current.name + "/" + d.UUID
	c := m.consoles[k]
	if c == nil {
		c = &vmConsole{key: k, title: d.DisplayName() + " · " + m.current.name}
		m.consoles[k] = c
	}
	return c
}

func (m *Model) openBrowser(d libvirt.Domain) tea.Cmd {
	c := m.consoleFor(d)
	if c.token == "" {
		token, err := m.opts.Web.Add(web.Console{Title: c.title, Dial: dialer(m.current, d.UUID)})
		if err != nil {
			m.setStatus(err.Error(), true)
			return nil
		}
		c.token = token
		m.byToken[token] = c
	}
	url, name := m.opts.Web.URL(c.token), d.DisplayName()
	m.setStatus("Opening "+name+" in your browser…", false)
	return func() tea.Msg {
		if err := platform.OpenURL(url); err != nil {
			// The console still works from a browser on this machine (ADR 0008).
			return statusMsg{text: fmt.Sprintf("Could not open the browser (%v). Open %s yourself.", err, url), err: true}
		}
		return statusMsg{text: "Opened " + name + " in your browser."}
	}
}

// openViewer starts the configured viewer on the VM's relay, opening the
// relay first if it isn't open (ADR 0009).
func (m *Model) openViewer(d libvirt.Domain) tea.Cmd {
	c := m.consoleFor(d)
	var cmds []tea.Cmd
	if c.relay == nil || c.relay.Closed() {
		k, send := c.key, m.send
		ln, err := relay.ListenTCP(dialer(m.current, d.UUID), relay.TCPOptions{
			FirstConnect: viewerFirstConnect,
			Idle:         viewerIdle,
			OnChange:     func(active int) { send(viewerActiveMsg{key: k, active: active}) },
			OnError:      func(err error) { send(viewerErrorMsg{key: k, err: err}) },
		})
		if err != nil {
			m.setStatus("Could not start the relay: "+err.Error(), true)
			return nil
		}
		c.relay, c.viewerActive = ln, 0
		cmds = append(cmds, func() tea.Msg {
			<-ln.Done()
			return relayClosedMsg{key: k, relay: ln}
		})
	}

	proc, err := viewer.Start(m.opts.Config.Viewer, viewer.Target{Host: "127.0.0.1", Port: c.relay.Port(), Title: c.title})
	if err != nil {
		m.setStatus(err.Error(), true)
		return tea.Batch(cmds...)
	}
	m.setStatus(fmt.Sprintf("Opened %s in %s.", d.DisplayName(), proc.Name()), false)
	cmds = append(cmds, func() tea.Msg {
		if err := proc.Wait(); err != nil {
			return statusMsg{text: err.Error(), err: true}
		}
		return nil
	})
	return tea.Batch(cmds...)
}

// dialer reaches a VM's VNC server through the node's SSH master.
func dialer(n *node, uuid string) relay.Dialer {
	return func(ctx context.Context) (*exec.Cmd, error) {
		// Read the port again on every connection: with autoport, a
		// restarted VM can get a new port and its old one can go to
		// another VM (ADR 0003).
		lookupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		d, err := n.lister.Lookup(lookupCtx, uuid)
		cancel()
		if err != nil {
			return nil, err
		}
		if d.VNC == nil {
			return nil, fmt.Errorf("%s has no VNC console reachable over TCP (%s)", d.DisplayName(), d.Graphics)
		}
		return n.ssh.ForwardCommand(ctx, d.VNC.Host, d.VNC.Port), nil
	}
}

func describe(err error, node string) string {
	if errors.Is(err, ssh.ErrNotConnected) {
		return fmt.Sprintf("The SSH connection to %s has closed. Press r to reconnect.", node)
	}
	var connErr *ssh.ConnError
	if errors.As(err, &connErr) {
		return fmt.Sprintf("SSH to %s failed: %s. Press r to try again.", node, connErr.Stderr)
	}
	return err.Error()
}

// connectCommand runs ssh in the foreground while the UI is suspended, so
// ssh can prompt on the terminal.
type connectCommand struct {
	cmd    *exec.Cmd
	node   string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func (c *connectCommand) SetStdin(r io.Reader)  { c.stdin = r }
func (c *connectCommand) SetStdout(w io.Writer) { c.stdout = w }
func (c *connectCommand) SetStderr(w io.Writer) { c.stderr = w }

func (c *connectCommand) Run() error {
	if c.stdin == nil {
		c.stdin = os.Stdin
	}
	fmt.Fprintf(c.stderr, "vncman: connecting to %s…\n", c.node)
	c.cmd.Stdout, c.cmd.Stderr = c.stdout, c.stderr
	err := c.cmd.Run()
	if err != nil {
		fmt.Fprintf(c.stderr, "\nvncman: could not connect to %s (%v). Press Enter to go back.", c.node, err)
		_, _ = bufio.NewReader(c.stdin).ReadString('\n')
	}
	return err
}
