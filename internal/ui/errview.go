package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/chalvinwz/lazyssm/internal/explain"
	"github.com/chalvinwz/lazyssm/internal/inventory"
)

// errorLabel names the full error text on the clipboard and in the status.
const errorLabel = "AWS error"

// lastError is what e shows: a failed connect or sign-in since the last
// fetch, else the fetch error.
func (m Model) lastError() (explain.Explanation, bool) {
	if m.failed != nil {
		return *m.failed, true
	}
	return m.explained, m.err != nil
}

// errorShown reports whether the detail panel shows the last error.
func (m Model) errorShown() bool {
	_, ok := m.lastError()
	return ok && m.showErr
}

// errorHeadline is the first line of the explanation, naming the profile when
// signing in again is the fix.
func (m Model) errorHeadline() string {
	if m.needsSignIn() {
		return "SSO session expired for " + profileLabel(m.profile)
	}
	return m.explained.Headline
}

// errorList explains the last fetch error in the list panel, below its header,
// with the keys most likely to get past it.
func (m Model) errorList(w int) []string {
	styles := m.styles()
	text := m.explained.Lines
	keys := [][2]string{{"R", "try another region"}, {"P", "try another profile"}, {"r", "try again"}}
	if m.explained.Service == "EC2" {
		// SSM-only browsing works without EC2 access; filters do not.
		if m.filter.Active() {
			keys = append([][2]string{{"f", "change or clear the filter"}}, keys...)
		}
		if m.src == inventory.SourceAllEC2 {
			keys = append([][2]string{{"t", "show SSM nodes only"}}, keys...)
		}
	}
	if m.needsSignIn() {
		text = []string{"Sign in to IAM Identity Center to load instances."}
		keys = [][2]string{{"L", "sign in (aws sso login)"}, {"P", "try another profile"}, {"r", "try again"}}
	}
	lines := []string{styles.failStyle.Render(truncate("✗ "+m.errorHeadline(), w)), ""}
	for _, line := range text {
		for _, wrapped := range strings.Split(lipgloss.Wrap(line, w-2, ""), "\n") {
			lines = append(lines, "  "+styles.detailValue.Render(wrapped))
		}
	}
	more := "show the full AWS error"
	if m.showErr {
		more = "hide the full AWS error"
	}
	lines = append(lines, "")
	for _, k := range append(keys, [2]string{"e", more}) {
		lines = append(lines, "  "+styles.keyStyle.Render(padRight(k[0], 3))+styles.faintStyle.Render(k[1]))
	}
	return lines
}

// errorDetail shows the full error in the detail panel: the facts lazyssm
// read from it, then the text as AWS sent it, wrapped to fit.
func (m Model) errorDetail(w, h int) string {
	styles := m.styles()
	x, _ := m.lastError()
	lines := []string{styles.panelTitleStyle.Render(errorLabel)}
	if x.Code != "" {
		lines = append(lines, styles.detailHdrName.Render(truncate(x.Code, w)))
	}
	lines = append(lines, "")
	for _, kv := range x.Facts {
		if kv[1] != "" {
			lines = append(lines, styles.detailKey.Render(padRight(kv[0], 8))+styles.detailValue.Render(truncate(kv[1], w-8)))
		}
	}
	keys := styles.keyStyle.Render("y") + styles.faintStyle.Render(" copy · ") +
		styles.keyStyle.Render("e") + styles.faintStyle.Render(" close")

	raw := strings.Split(lipgloss.Wrap(x.Raw, w, " :/,"), "\n")
	room := h - len(lines) - 3 // blank lines around the text, and the keys
	if room < len(raw) {
		raw = raw[:max(0, room)]
		if len(raw) > 0 {
			raw[len(raw)-1] = truncate(raw[len(raw)-1]+"…", w)
		}
	}
	lines = append(lines, "")
	for _, line := range raw {
		lines = append(lines, styles.faintStyle.Render(line))
	}
	lines = append(lines, "", keys)
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m Model) copyError() (tea.Model, tea.Cmd) {
	x, _ := m.lastError()
	cmd, viaTerminal := m.copyText(errorLabel, x.Raw)
	if viaTerminal {
		m.status = "sent the " + errorLabel + " to the terminal clipboard"
	}
	return m, cmd
}
