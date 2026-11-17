package tui

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"github.com/ludwighansson/vncman/internal/config"
	"github.com/ludwighansson/vncman/internal/libvirt"
)

var (
	appStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	dimStyle  = lipgloss.NewStyle().Faint(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	pickStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
)

const maxStatusLines = 4

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("8"))
	s.Selected = lipgloss.NewStyle().Bold(true).Reverse(true)
	return s
}

// column is one column of the VM table.
type column struct {
	title string
	width int
	max   int
	min   int
	value func(m *Model, d libvirt.Domain) string
}

func allColumns() []column {
	return []column{
		{title: " ", max: 1, min: 1, value: marker},
		{title: "Name", max: 40, min: 12, value: func(_ *Model, d libvirt.Domain) string { return d.DisplayName() }},
		{title: "Project", max: 24, min: 8, value: func(_ *Model, d libvirt.Domain) string { return d.Project() }},
		{title: "Flavor", max: 20, min: 8, value: func(_ *Model, d libvirt.Domain) string { return d.Flavor() }},
		{title: "Domain", max: 32, min: 10, value: libvirtName},
		{title: "UUID", max: 36, min: 36, value: func(_ *Model, d libvirt.Domain) string { return d.UUID }},
		{title: "VNC", max: 28, min: 5, value: func(_ *Model, d libvirt.Domain) string { return vncText(d) }},
	}
}

func marker(m *Model, d libvirt.Domain) string {
	if m.current == nil {
		return " "
	}
	if c := m.consoles[m.current.name+"/"+d.UUID]; c != nil && c.open() {
		return "●"
	}
	return " "
}

// libvirtName is shown only when it differs from the display name, as it
// does for Nova instances.
func libvirtName(_ *Model, d libvirt.Domain) string {
	if d.Name == d.DisplayName() {
		return ""
	}
	return d.Name
}

func vncText(d libvirt.Domain) string {
	if d.VNC == nil {
		return d.Graphics
	}
	if d.VNC.Host == "127.0.0.1" {
		return strconv.Itoa(d.VNC.Port)
	}
	return net.JoinHostPort(d.VNC.Host, strconv.Itoa(d.VNC.Port))
}

// fitColumns picks the columns that have data on this node and sizes them
// to the terminal width.
func (m *Model) fitColumns() []column {
	var hasProject, hasFlavor, hasNova bool
	for _, d := range m.domains {
		hasProject = hasProject || d.Project() != ""
		hasFlavor = hasFlavor || d.Flavor() != ""
		hasNova = hasNova || d.DisplayName() != d.Name
	}
	var cols []column
	for _, c := range allColumns() {
		switch {
		case c.title == "Project" && !hasProject,
			c.title == "Flavor" && !hasFlavor,
			c.title == "Domain" && !hasNova:
			continue
		}
		c.width = lipgloss.Width(c.title)
		for _, d := range m.domains {
			c.width = max(c.width, lipgloss.Width(c.value(m, d)))
		}
		c.width = min(c.width, c.max)
		cols = append(cols, c)
	}

	// Each cell has one space of padding on both sides.
	total := func() int {
		t := 0
		for _, c := range cols {
			t += c.width + 2
		}
		return t
	}
	if m.width <= 0 || total() <= m.width {
		return cols
	}
	// Too wide: drop the UUID column first (the filter still matches it),
	// then shrink the text columns.
	for i, c := range cols {
		if c.title == "UUID" {
			cols = append(cols[:i], cols[i+1:]...)
			break
		}
	}
	for _, title := range []string{"Domain", "Project", "Flavor", "Name", "VNC"} {
		over := total() - m.width
		if over <= 0 {
			break
		}
		for i := range cols {
			if cols[i].title == title {
				cols[i].width = max(cols[i].min, cols[i].width-over)
			}
		}
	}
	return cols
}

// layoutTable rebuilds the table's columns, rows and size.
func (m *Model) layoutTable() {
	m.cols = m.fitColumns()
	tcols := make([]table.Column, len(m.cols))
	for i, c := range m.cols {
		tcols[i] = table.Column{Title: c.title, Width: c.width}
	}
	rows := make([]table.Row, len(m.visible))
	for i, d := range m.visible {
		row := make(table.Row, len(m.cols))
		for j, c := range m.cols {
			row[j] = c.value(m, d)
		}
		rows[i] = row
	}
	cursor := m.table.Cursor()
	// Rows must never have more cells than there are columns.
	m.table.SetRows(nil)
	m.table.SetColumns(tcols)
	m.table.SetRows(rows)
	if m.width > 0 {
		m.table.SetWidth(m.width)
	}
	if m.height > 0 {
		// Title, filter, status and help lines surround the table.
		m.table.SetHeight(max(4, m.height-3-m.statusHeight()))
	}
	m.table.SetCursor(min(max(cursor, 0), max(len(rows)-1, 0)))
}

// applyFilter recomputes the visible VMs, keeping the selection if it is
// still visible.
func (m *Model) applyFilter() {
	uuid := ""
	if d, ok := m.selected(); ok {
		uuid = d.UUID
	}
	m.visible = filterDomains(m.domains, m.filter.Value())
	m.layoutTable()
	m.table.SetCursor(0)
	for i, d := range m.visible {
		if d.UUID == uuid {
			m.table.SetCursor(i)
			break
		}
	}
}

// filterDomains returns the domains that match every word of query in any
// of their name, libvirt name, UUID, project or flavor.
func filterDomains(domains []libvirt.Domain, query string) []libvirt.Domain {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return domains
	}
	var out []libvirt.Domain
	for _, d := range domains {
		text := strings.ToLower(strings.Join([]string{d.DisplayName(), d.Name, d.UUID, d.Project(), d.Flavor()}, " "))
		match := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, d)
		}
	}
	return out
}

type choice struct{ name, note string }

// pickChoices returns the nodes to offer in the node picker: what was
// typed, then recent nodes, then configured ones, filtered by what was typed.
func (m *Model) pickChoices() []choice {
	typed := strings.TrimSpace(m.pick.Value())
	query := strings.ToLower(typed)
	recent := make(map[string]bool)
	for _, r := range m.opts.State.Recent {
		recent[r] = true
	}
	names := append([]string{}, m.opts.State.Recent...)
	for _, n := range m.opts.Config.Nodes {
		names = append(names, n.Name)
	}

	var out []choice
	seen := make(map[string]bool)
	exact := false
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		var notes []string
		if g := m.opts.Config.Group(name); g != "" {
			notes = append(notes, g)
		}
		if recent[name] {
			notes = append(notes, "recent")
		}
		note := strings.Join(notes, " · ")
		if query != "" && !strings.Contains(strings.ToLower(name+" "+note), query) {
			continue
		}
		exact = exact || name == typed
		out = append(out, choice{name: name, note: note})
	}
	if typed != "" && !exact {
		out = append([]choice{{name: typed, note: "connect"}}, out...)
	}
	return out
}

// View implements tea.Model.
func (m *Model) View() string {
	switch m.mode {
	case modePick:
		return m.pickView()
	case modePassword:
		return m.passwordView()
	case modeLoading:
		if len(m.domains) == 0 {
			return m.header() + "\n\n" + m.spinner.View() + " " + m.loading + "…\n"
		}
	}
	return m.listView()
}

func (m *Model) header() string {
	parts := []string{appStyle.Render("VNCMan")}
	if m.current != nil {
		parts = append(parts, m.current.name)
		if m.mode != modeLoading || len(m.domains) > 0 {
			parts = append(parts, fmt.Sprintf("%d running", len(m.domains)))
		}
		if len(m.visible) != len(m.domains) {
			parts = append(parts, fmt.Sprintf("%d shown", len(m.visible)))
		}
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

func (m *Model) listView() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.table.View())
	b.WriteString("\n")
	if m.filtering || m.filter.Value() != "" {
		b.WriteString(m.filter.View())
	}
	b.WriteString("\n")
	b.WriteString(m.statusView())
	b.WriteString("\n")
	b.WriteString(m.truncate(dimStyle.Render(m.help())))
	return b.String()
}

func (m *Model) statusView() string {
	switch m.mode {
	case modeLoading:
		return m.spinner.View() + " " + m.loading + "…"
	case modeConfirmQuit:
		n := m.openConsoles()
		noun := "console is"
		if n != 1 {
			noun = "consoles are"
		}
		return m.truncate(warnStyle.Render(fmt.Sprintf("%d %s open. Quit and close them? (y/n)", n, noun)))
	}
	if m.status == "" {
		return ""
	}
	style := okStyle
	if m.statusErr {
		style = errStyle
	}
	if m.width > 0 {
		style = style.Width(m.width)
	}
	lines := strings.Split(style.Render(m.status), "\n")
	if len(lines) > maxStatusLines {
		lines = lines[:maxStatusLines]
	}
	return strings.Join(lines, "\n")
}

// statusHeight is the number of lines the status takes in the list view.
func (m *Model) statusHeight() int {
	if m.mode == modeLoading || m.mode == modeConfirmQuit {
		return 1
	}
	return max(1, lipgloss.Height(m.statusView()))
}

func (m *Model) help() string {
	if m.filtering {
		return "type to filter · ↑↓ move · ⏎ done · esc clear"
	}
	open := "⏎ browser · v viewer"
	if m.opts.Config.Console == config.ConsoleViewer {
		open = "⏎ viewer · b browser"
	}
	return "↑↓ move · / filter · " + open + " · r refresh · n node · q quit"
}

func (m *Model) pickView() string {
	var b strings.Builder
	b.WriteString(appStyle.Render("VNCMan") + dimStyle.Render(" · open a node") + "\n\n")
	b.WriteString(m.pick.View() + "\n\n")
	choices := m.pickChoices()
	if len(choices) == 0 {
		b.WriteString(dimStyle.Render("Type a host name, or an alias from ~/.ssh/config.") + "\n")
	}
	limit := len(choices)
	if m.height > 0 {
		limit = min(limit, max(1, m.height-8))
	}
	for i, c := range choices[:limit] {
		line := fmt.Sprintf(" %-30s %s ", c.name, c.note)
		if i == m.pickCursor {
			b.WriteString(pickStyle.Render(line) + "\n")
		} else {
			b.WriteString(" " + c.name + strings.Repeat(" ", max(1, 31-lipgloss.Width(c.name))) + dimStyle.Render(c.note) + "\n")
		}
	}
	if m.status != "" {
		style := okStyle
		if m.statusErr {
			style = errStyle
		}
		b.WriteString("\n" + m.truncate(style.Render(m.status)) + "\n")
	}
	back := "esc quit"
	if m.current != nil {
		back = "esc back"
	}
	b.WriteString("\n" + dimStyle.Render("↑↓ choose · ⏎ connect · "+back))
	return b.String()
}

func (m *Model) passwordView() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n\n")
	fmt.Fprintf(&b, "sudo on %s needs your password to list VMs.\n", m.current.name)
	b.WriteString(dimStyle.Render("VNCMan keeps it in memory for this node until it quits.") + "\n\n")
	b.WriteString(m.password.View() + "\n")
	if m.passwordMsg != "" {
		b.WriteString("\n" + errStyle.Render(m.passwordMsg) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("⏎ submit · esc cancel"))
	return b.String()
}

func (m *Model) truncate(s string) string {
	if m.width <= 0 {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(s)
}
