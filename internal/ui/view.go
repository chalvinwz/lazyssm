package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/chalvinwz/lazyssm/internal/inventory"
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
	background := m.styles().background.GetBackground()
	if _, unset := background.(lipgloss.NoColor); !unset {
		v.BackgroundColor = background
	}
	return v
}

func (m Model) render() string {
	var content string
	switch {
	case m.showPreflight:
		content = m.renderPreflight()
	case m.mode == modeHelp:
		content = m.renderHelp()
	case m.width < minWidth || m.height < minHeight:
		content = m.renderCompact()
	default:
		content = m.renderSplit()
	}
	if m.styles().name == "default" {
		return content
	}
	background := m.styles().background
	if m.width > 0 {
		background = background.Width(m.width)
	}
	return background.Render(content)
}

// renderSplit draws the title bar, the list and detail panels, and the footer.
func (m Model) renderSplit() string {
	title := lipgloss.NewStyle().Width(m.width).Render(m.titleBar())
	footer := lipgloss.NewStyle().Width(m.width).Render(m.footerBar())

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
	listBox := m.boxStyle(listActive).Width(listOuter).Height(bodyH).
		Render(m.listContent(listInner, innerH))
	detailBox := m.styles().panelStyle.Width(detailOuter).Height(bodyH).
		Render(m.detailContent(detailInner, innerH))

	body := lipgloss.JoinHorizontal(lipgloss.Top, listBox, detailBox)
	return strings.Join([]string{title, body, footer}, "\n")
}

func (m Model) boxStyle(active bool) lipgloss.Style {
	if active {
		return m.styles().panelActiveStyle
	}
	return m.styles().panelStyle
}

func (m Model) titleBar() string {
	src := "SSM"
	if m.src == inventory.SourceAllEC2 {
		src = "EC2"
	}
	profile := profileLabel(m.profile)
	if m.account != "" {
		profile += " (" + m.account + ")"
	}
	meta := fmt.Sprintf(" %s · %s · %s", profile, m.clients.Region(), src)
	if m.filter.Active() {
		meta += "  filter:" + m.filter.String()
	}
	left := m.styles().titleStyle.Render("lazyssm")
	return left + m.styles().metaStyle.Render(meta)
}

func (m Model) listContent(w, h int) string {
	if m.mode == modePicker {
		return m.pickerList(w, h)
	}
	count := len(m.visible)
	header := m.styles().panelTitleStyle.Render(fmt.Sprintf("instances (%d)", count))
	var headings []string
	columns := m.listColumns(w)
	for _, column := range columns {
		headings = append(headings, padRight(truncate(strings.ToUpper(column.name), column.width), column.width))
	}
	header += "\n" + m.styles().dimStyle.Render("   "+strings.Join(headings, " "))
	rowsArea := h - lipgloss.Height(header)
	if rowsArea < 1 {
		rowsArea = 1
	}

	if m.loading {
		return header + "\n" + m.styles().dimStyle.Render("loading…")
	}
	if count == 0 {
		if m.err != nil {
			lines := append([]string{header}, m.errorList(w)...)
			return strings.Join(lines[:min(len(lines), h)], "\n")
		}
		return header + "\n" + m.styles().offlineStyle.Render(truncate("no instances — f to filter, t to toggle source", w))
	}

	start := 0
	if m.cursor >= rowsArea {
		start = m.cursor - rowsArea + 1
	}
	end := min(count, start+rowsArea)

	lines := make([]string, 0, rowsArea+1)
	lines = append(lines, header)
	for i := start; i < end; i++ {
		lines = append(lines, m.rowLine(i, m.visible[i], w, columns))
	}
	return strings.Join(lines, "\n")
}

func instanceState(state string) string {
	if state == "" {
		return "—"
	}
	return state
}

func instanceAge(launch, now time.Time) string {
	if launch.IsZero() {
		return "—"
	}
	minutes := max(int64(0), int64(now.Sub(launch)/time.Minute))
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh %dm", hours, minutes%60)
	}
	return fmt.Sprintf("%dd %dh", hours/24, hours%24)
}

func (m Model) detailContent(w, h int) string {
	if m.mode == modePicker {
		return m.pickerDetail(w, h)
	}
	if m.errorShown() {
		return m.errorDetail(w, h)
	}
	title := m.styles().panelTitleStyle.Render("detail")
	if m.cursor >= len(m.visible) {
		return title + "\n" + m.styles().dimStyle.Render("—")
	}
	in := m.visible[m.cursor]

	var lines []string
	lines = append(lines, title)
	name := in.Name
	if name == "" {
		name = "(no Name)"
	}
	lines = append(lines, m.styles().detailHdrName.Render(truncate(name, w)))
	lines = append(lines, m.styles().faintStyle.Render(truncate(in.InstanceID, w)))
	lines = append(lines, "")

	ping := in.PingStatus
	if ping == "" {
		ping = "—"
	}
	readiness := m.styles().offlineStyle.Render("not-ready")
	if in.SSMReady {
		readiness = m.styles().onlineStyle.Render("ready")
	}
	add := func(k, v string) {
		if v == "" {
			v = "—"
		}
		lines = append(lines, m.styles().detailKey.Render(padRight(k, 7))+m.styles().detailValue.Render(truncate(v, w-7)))
	}
	lines = append(lines, m.styles().detailKey.Render(padRight("ssm", 7))+readiness)
	add("ping", ping)
	lines = append(lines, m.styles().detailKey.Render(padRight("state", 7))+m.stateStyle(in.State).Render(truncate(instanceState(in.State), w-7)))
	add("age", instanceAge(in.LaunchTime, time.Now()))
	add("ip", in.IP)
	add("os", in.PlatformType)
	add("agent", in.AgentStatus)
	pinned := "no"
	if in.Pinned {
		pinned = m.styles().pinStyle.Render("★ yes")
	}
	lines = append(lines, m.styles().detailKey.Render(padRight("pinned", 7))+pinned)

	// Tags fill remaining vertical space.
	if len(in.Tags) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.styles().detailKey.Render("tags"))
		for _, kv := range sortedTags(in.Tags) {
			if len(lines) >= h {
				break
			}
			lines = append(lines, m.styles().faintStyle.Render(truncate("  "+kv, w)))
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
		return m.styles().keyStyle.Render("filter") + m.styles().dimStyle.Render(" (tag:K=V name:prefix · ⏎ apply · esc cancel)  ") + m.filterInput.View()
	case modeSearch:
		hint := m.styles().dimStyle.Render(" (↑↓ move · ⏎ connect · esc clear)  ")
		return m.styles().keyStyle.Render("search") + hint + m.searchInput.View()
	case modePicker:
		return m.pickerFooter()
	default:
		status := m.status
		if status == "" {
			status = fmt.Sprintf("%d shown", len(m.visible))
		}
		// One line only: a long status would wrap and push the panels up.
		width := m.width
		if width <= 0 {
			width = lipgloss.Width(status) + 64 // no size yet: nothing to fit
		}
		statusR := m.styles().dimStyle.Render(truncate(status, width))
		if m.err != nil || m.failed != nil {
			// Show failures in the fail style. After a failed fetch the list
			// still shows the last successful result rather than blanking.
			suffix := ""
			if m.err != nil && len(m.visible) > 0 {
				suffix += " (showing last-known)"
			}
			switch {
			case m.needsSignIn() || (m.failed != nil && m.failed.SignIn):
				suffix += " · L sign in"
			case !m.errorShown():
				suffix += " · e details"
			}
			status = truncate(status, width-lipgloss.Width(suffix)) + suffix
			statusR = m.styles().failStyle.Render(truncate(status, width))
		}
		return statusR + "\n" + m.footerHints()
	}
}

func (m Model) renderCompact() string {
	// Tiny terminals get a minimal fallback without borders, which would break.
	var b strings.Builder
	b.WriteString(m.styles().titleStyle.Render("lazyssm"))
	b.WriteString("\n")
	if m.mode == modePicker {
		b.WriteString(m.pickerList(m.width, m.height-2) + "\n" + m.pickerFooter())
		return b.String()
	}
	if m.loading {
		b.WriteString(m.styles().dimStyle.Render("loading…"))
		return b.String()
	}
	if m.err != nil && len(m.visible) == 0 {
		b.WriteString(m.styles().failStyle.Render(truncate("✗ "+m.errorHeadline(), m.width)))
		return b.String()
	}
	columns := m.listColumns(m.width)
	for i, in := range m.visible {
		if i >= m.height-3 {
			break
		}
		b.WriteString(m.rowLine(i, in, m.width, columns) + "\n")
	}
	return b.String()
}

func (m Model) renderPreflight() string {
	var b strings.Builder
	b.WriteString(m.styles().titleStyle.Render("lazyssm — preflight"))
	b.WriteString("\n\n")
	for _, c := range m.preflight {
		mark := m.styles().okStyle.Render("✓")
		if !c.OK {
			mark = m.styles().failStyle.Render("✗")
		}
		fmt.Fprintf(&b, "  %s %s %s\n", mark, m.styles().detailValue.Render(padRight(c.Name, 22)), m.styles().dimStyle.Render(c.Detail))
		if !c.OK && c.Fix != "" {
			for _, fl := range strings.Split(c.Fix, "\n") {
				b.WriteString(m.styles().faintStyle.Render("      " + fl))
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("\n")
	b.WriteString(m.styles().keyStyle.Render("r") + m.styles().faintStyle.Render(" re-check · ") +
		m.styles().keyStyle.Render("esc/enter") + m.styles().faintStyle.Render(" dismiss · ") +
		m.styles().keyStyle.Render("ctrl+c") + m.styles().faintStyle.Render(" quit"))
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
