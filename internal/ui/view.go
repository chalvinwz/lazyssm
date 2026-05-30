package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

// AWS amber/orange theme.
var (
	cAccent  = lipgloss.Color("#FF9900") // AWS orange
	cAccent2 = lipgloss.Color("#FFB454") // lighter amber
	cDim     = lipgloss.Color("#8B949E")
	cFaint   = lipgloss.Color("#586069")
	cOnline  = lipgloss.Color("#3FB950")
	cOffline = lipgloss.Color("#F85149")
	cPin     = lipgloss.Color("#F0B72F")
	cInk     = lipgloss.Color("#1A1A1A")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent).Padding(0, 1)
	metaStyle  = lipgloss.NewStyle().Foreground(cDim)
	dimStyle   = lipgloss.NewStyle().Foreground(cDim)
	faintStyle = lipgloss.NewStyle().Foreground(cFaint)
	keyStyle   = lipgloss.NewStyle().Foreground(cAccent2)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cFaint)
	panelActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(cAccent)
	panelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(cAccent)

	selStyle      = lipgloss.NewStyle().Bold(true).Foreground(cInk).Background(cAccent)
	nameStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E6EDF3"))
	onlineStyle   = lipgloss.NewStyle().Foreground(cOnline)
	offlineStyle  = lipgloss.NewStyle().Foreground(cOffline)
	pinStyle      = lipgloss.NewStyle().Foreground(cPin)
	okStyle       = lipgloss.NewStyle().Foreground(cOnline)
	failStyle     = lipgloss.NewStyle().Foreground(cOffline)
	detailKey     = lipgloss.NewStyle().Foreground(cDim)
	detailValue   = lipgloss.NewStyle().Foreground(lipgloss.Color("#E6EDF3"))
	detailHdrName = lipgloss.NewStyle().Bold(true).Foreground(cAccent2)
)

const (
	minWidth  = 60
	minHeight = 12

	// Detail panel sizing: target a share of the terminal width, clamped to a
	// readable column range.
	detailPctWidth = 38 // % of terminal width
	detailMinCols  = 24
	detailMaxCols  = 44
)

// View renders the UI.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	if m.showPreflight {
		return m.renderPreflight()
	}
	if m.mode == modeHelp {
		return m.renderHelp()
	}
	if m.width < minWidth || m.height < minHeight {
		return m.renderCompact()
	}
	return m.renderSplit()
}

// renderSplit draws the title bar, list+detail panels, and footer.
func (m Model) renderSplit() string {
	title := m.titleBar()
	footer := m.footerBar()

	bodyH := m.height - lipgloss.Height(title) - lipgloss.Height(footer)
	if bodyH < 3 {
		bodyH = 3
	}
	innerH := bodyH - 2 // panel top+bottom borders

	detailOuter := clampInt(m.width*detailPctWidth/100, detailMinCols, detailMaxCols)
	listOuter := m.width - detailOuter
	listInner := listOuter - 2
	detailInner := detailOuter - 2

	listActive := m.mode != modeFilter
	listBox := boxStyle(listActive).Width(listInner).Height(innerH).
		Render(m.listContent(listInner, innerH))
	detailBox := panelStyle.Width(detailInner).Height(innerH).
		Render(m.detailContent(detailInner, innerH))

	body := lipgloss.JoinHorizontal(lipgloss.Top, listBox, detailBox)
	return strings.Join([]string{title, body, footer}, "\n")
}

func boxStyle(active bool) lipgloss.Style {
	if active {
		return panelActiveStyle
	}
	return panelStyle
}

func (m Model) titleBar() string {
	src := "SSM"
	if m.src == inventory.SourceAllEC2 {
		src = "EC2"
	}
	profile := m.profile
	if profile == "" {
		profile = "default"
	}
	meta := fmt.Sprintf(" %s · %s · %s", profile, m.clients.Region(), src)
	if m.filter.Active() {
		meta += "  filter:" + m.filter.String()
	}
	left := titleStyle.Render("lazyssm")
	return left + metaStyle.Render(meta)
}

func (m Model) listContent(w, h int) string {
	count := len(m.visible)
	header := panelTitleStyle.Render(fmt.Sprintf("instances (%d)", count))
	rowsArea := h - 1
	if rowsArea < 1 {
		rowsArea = 1
	}

	if m.loading {
		return header + "\n" + dimStyle.Render("loading…")
	}
	if count == 0 {
		msg := "no instances — f to filter, t to toggle source"
		if m.err != nil {
			msg = "error: " + m.err.Error()
		}
		return header + "\n" + offlineStyle.Render(truncate(msg, w))
	}

	start := 0
	if m.cursor >= rowsArea {
		start = m.cursor - rowsArea + 1
	}
	end := min(count, start+rowsArea)

	lines := make([]string, 0, rowsArea+1)
	lines = append(lines, header)
	for i := start; i < end; i++ {
		lines = append(lines, m.rowLine(i, m.visible[i], w))
	}
	return strings.Join(lines, "\n")
}

func (m Model) rowLine(i int, in inventory.Instance, w int) string {
	pin := " "
	if in.Pinned {
		pin = "★"
	}
	name := in.Name
	if name == "" {
		name = "(no Name)"
	}
	dot := "○"
	if in.SSMReady {
		dot = "●"
	}
	// pin/dot are East-Asian ambiguous-width glyphs: lipgloss.Width reports 1 but
	// the box layout counts 2, which wraps the row. Reserve 2 cols per marker —
	// over-reserving only leaves a trailing gap, it never wraps.
	const markerW = 2
	fixed := 1 + markerW + 1 + 1 + markerW // leading space + pin + gaps + dot
	nameW := w - fixed
	if nameW < 4 {
		nameW = 4
	}
	name = padRight(truncate(name, nameW), nameW)

	if i == m.cursor {
		line := fmt.Sprintf(" %s %s %s", pin, name, dot)
		return selStyle.Width(w).Render(line)
	}
	pinR := faintStyle.Render(pin)
	if in.Pinned {
		pinR = pinStyle.Render(pin)
	}
	dotR := offlineStyle.Render(dot)
	if in.SSMReady {
		dotR = onlineStyle.Render(dot)
	}
	return fmt.Sprintf(" %s %s %s", pinR, nameStyle.Render(name), dotR)
}

func (m Model) detailContent(w, h int) string {
	title := panelTitleStyle.Render("detail")
	if m.cursor >= len(m.visible) {
		return title + "\n" + dimStyle.Render("—")
	}
	in := m.visible[m.cursor]

	var lines []string
	lines = append(lines, title)
	name := in.Name
	if name == "" {
		name = "(no Name)"
	}
	lines = append(lines, detailHdrName.Render(truncate(name, w)))
	lines = append(lines, faintStyle.Render(truncate(in.InstanceID, w)))
	lines = append(lines, "")

	ping := in.PingStatus
	if ping == "" {
		ping = "—"
	}
	readiness := offlineStyle.Render("not-ready")
	if in.SSMReady {
		readiness = onlineStyle.Render("ready")
	}
	add := func(k, v string) {
		if v == "" {
			v = "—"
		}
		lines = append(lines, detailKey.Render(padRight(k, 7))+detailValue.Render(truncate(v, w-7)))
	}
	add("ssm", "")
	lines[len(lines)-1] = detailKey.Render(padRight("ssm", 7)) + readiness
	add("ping", ping)
	add("state", in.State)
	add("ip", in.IP)
	add("os", in.PlatformType)
	add("agent", in.AgentStatus)
	pinned := "no"
	if in.Pinned {
		pinned = pinStyle.Render("★ yes")
	}
	lines = append(lines, detailKey.Render(padRight("pinned", 7))+pinned)

	// Tags fill remaining vertical space.
	if len(in.Tags) > 0 {
		lines = append(lines, "")
		lines = append(lines, detailKey.Render("tags"))
		for _, kv := range sortedTags(in.Tags) {
			if len(lines) >= h {
				break
			}
			lines = append(lines, faintStyle.Render(truncate("  "+kv, w)))
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m Model) footerBar() string {
	switch m.mode {
	case modeFilter:
		return keyStyle.Render("filter") + dimStyle.Render(" (tag:K=V name:prefix · ⏎ apply · esc cancel)  ") + m.filterInput.View()
	case modeSearch:
		hint := dimStyle.Render(" (↑↓ move · ⏎ connect · esc clear)  ")
		return keyStyle.Render("search") + hint + m.searchInput.View()
	default:
		status := m.status
		if status == "" {
			status = fmt.Sprintf("%d shown", len(m.visible))
		}
		statusR := dimStyle.Render(status)
		if m.err != nil {
			// Surface fetch failures distinctly; the list still shows the
			// last successful result rather than blanking.
			if len(m.visible) > 0 {
				status += " (showing last-known)"
			}
			statusR = failStyle.Render(status)
		}
		hints := []string{"↑↓ move", "f filter", "/ search", "t toggle", "p pin", "r refresh", "⏎ connect", "? help", "q quit"}
		return statusR + "\n" + renderHints(hints)
	}
}

func renderHints(pairs []string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		fields := strings.SplitN(p, " ", 2)
		parts[i] = keyStyle.Render(fields[0]) + faintStyle.Render(" "+fields[1])
	}
	return strings.Join(parts, faintStyle.Render(" · "))
}

func (m Model) renderCompact() string {
	// Minimal fallback for tiny terminals — no borders to avoid breakage.
	var b strings.Builder
	b.WriteString(titleStyle.Render("lazyssm"))
	b.WriteString("\n")
	if m.loading {
		b.WriteString(dimStyle.Render("loading…"))
		return b.String()
	}
	for i, in := range m.visible {
		if i >= m.height-3 {
			break
		}
		mark := "  "
		if i == m.cursor {
			mark = "> "
		}
		name := in.Name
		if name == "" {
			name = in.InstanceID
		}
		b.WriteString(mark + truncate(name, m.width-2) + "\n")
	}
	return b.String()
}

func (m Model) renderHelp() string {
	rows := [][2]string{
		{"j / ↓", "move down"},
		{"k / ↑", "move up"},
		{"g / G", "top / bottom"},
		{"f", "filter by tag/name (server-side)"},
		{"/", "fuzzy search (↑↓ to move, ⏎ connects)"},
		{"t", "toggle SSM-only ↔ all EC2"},
		{"p", "pin / unpin selected"},
		{"r", "refresh inventory"},
		{"enter", "connect (SSM shell)"},
		{"?", "toggle this help"},
		{"q / ctrl+c", "quit"},
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("lazyssm — keybindings"))
	b.WriteString("\n\n")
	for _, r := range rows {
		b.WriteString("  " + keyStyle.Render(padRight(r[0], 12)) + detailValue.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("press any key to return"))
	return b.String()
}

func (m Model) renderPreflight() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("lazyssm — preflight"))
	b.WriteString("\n\n")
	for _, c := range m.preflight {
		mark := okStyle.Render("✓")
		if !c.OK {
			mark = failStyle.Render("✗")
		}
		fmt.Fprintf(&b, "  %s %s %s\n", mark, detailValue.Render(padRight(c.Name, 22)), dimStyle.Render(c.Detail))
		if !c.OK && c.Fix != "" {
			for _, fl := range strings.Split(c.Fix, "\n") {
				b.WriteString(faintStyle.Render("      " + fl))
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n")
	b.WriteString(keyStyle.Render("r") + faintStyle.Render(" re-check · ") +
		keyStyle.Render("esc/enter") + faintStyle.Render(" dismiss · ") +
		keyStyle.Render("ctrl+c") + faintStyle.Render(" quit"))
	return b.String()
}

// --- helpers ---

func sortedTags(tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+tags[k])
	}
	return out
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return string([]rune(s)[:n])
	}
	r := []rune(s)
	if len(r) > n-1 {
		r = r[:n-1]
	}
	return string(r) + "…"
}

func padRight(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
