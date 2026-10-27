package ui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

type palette struct {
	accent, accent2, dim, faint, online, offline, pin, ink, text, background string
}

type themeStyles struct {
	name                                                                         string
	titleStyle, metaStyle, dimStyle, faintStyle, keyStyle                        lipgloss.Style
	panelStyle, panelActiveStyle, panelTitleStyle                                lipgloss.Style
	selStyle, nameStyle, onlineStyle, offlineStyle, pinStyle, okStyle, failStyle lipgloss.Style
	detailKey, detailValue, detailHdrName, background                            lipgloss.Style
}

// ValidTheme reports whether name identifies a built-in preset.
func ValidTheme(name string) bool {
	switch name {
	case "default", "tokyo-night", "dracula", "nord", "catppuccin-mocha":
		return true
	default:
		return false
	}
}

func makeStyles(name string, transparent bool) themeStyles {
	p := palette{"#FF9900", "#FFB454", "#8B949E", "#586069", "#3FB950", "#F85149", "#F0B72F", "#1A1A1A", "#E6EDF3", ""}
	switch name {
	case "tokyo-night":
		p = palette{"#7AA2F7", "#BB9AF7", "#A9B1D6", "#565F89", "#9ECE6A", "#F7768E", "#E0AF68", "#1A1B26", "#C0CAF5", "#1A1B26"}
	case "dracula":
		p = palette{"#BD93F9", "#8BE9FD", "#BFBFBF", "#6272A4", "#50FA7B", "#FF5555", "#F1FA8C", "#282A36", "#F8F8F2", "#282A36"}
	case "nord":
		p = palette{"#88C0D0", "#81A1C1", "#D8DEE9", "#4C566A", "#A3BE8C", "#BF616A", "#EBCB8B", "#2E3440", "#ECEFF4", "#2E3440"}
	case "catppuccin-mocha":
		p = palette{"#CBA6F7", "#89B4FA", "#BAC2DE", "#6C7086", "#A6E3A1", "#F38BA8", "#F9E2AF", "#1E1E2E", "#CDD6F4", "#1E1E2E"}
	default:
		name = "default"
	}
	if transparent {
		p.background = ""
	}
	fg := func(c string) lipgloss.Style {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(c))
		if p.background != "" {
			style = style.Background(lipgloss.Color(p.background))
		}
		return style
	}
	s := themeStyles{name: name}
	s.titleStyle = fg(p.ink).Bold(true).Background(lipgloss.Color(p.accent)).Padding(0, 1)
	s.metaStyle = fg(p.dim)
	s.dimStyle = fg(p.dim)
	s.faintStyle = fg(p.faint)
	s.keyStyle = fg(p.accent2)
	s.panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.faint))
	if p.background != "" {
		s.panelStyle = s.panelStyle.Background(lipgloss.Color(p.background)).BorderBackground(lipgloss.Color(p.background))
	}
	s.panelActiveStyle = s.panelStyle.BorderForeground(lipgloss.Color(p.accent))
	s.panelTitleStyle = fg(p.accent).Bold(true)
	s.selStyle = fg(p.ink).Bold(true).Background(lipgloss.Color(p.accent))
	s.nameStyle = fg(p.text)
	s.onlineStyle = fg(p.online)
	s.offlineStyle = fg(p.offline)
	s.pinStyle = fg(p.pin)
	s.okStyle = s.onlineStyle
	s.failStyle = s.offlineStyle
	s.detailKey = fg(p.dim)
	s.detailValue = fg(p.text)
	s.detailHdrName = fg(p.accent2).Bold(true)
	s.background = lipgloss.NewStyle()
	if p.background != "" {
		s.background = s.background.Background(lipgloss.Color(p.background))
	}
	return s
}

func (m Model) styles() themeStyles {
	if m.appearance == nil {
		return makeStyles("default", false)
	}
	return *m.appearance
}

// WithTheme overrides the appearance without changing the persisted configuration.
func (m Model) WithTheme(name string) Model {
	transparent := m.store != nil && m.store.TransparentBackground
	s := makeStyles(name, transparent)
	m.appearance = &s
	inputStyles := textinput.DefaultDarkStyles()
	if s.name != "default" {
		state := textinput.StyleState{Text: s.detailValue, Placeholder: s.faintStyle, Suggestion: s.dimStyle, Prompt: s.keyStyle}
		inputStyles.Focused = state
		inputStyles.Blurred = state
		inputStyles.Cursor.Color = s.keyStyle.GetForeground()
	}
	m.filterInput.SetStyles(inputStyles)
	m.searchInput.SetStyles(inputStyles)
	return m
}

func (m Model) stateStyle(state string) lipgloss.Style {
	switch state {
	case "running":
		return m.styles().onlineStyle
	case "pending", "stopping", "shutting-down":
		return m.styles().pinStyle
	case "stopped", "terminated":
		return m.styles().offlineStyle
	default:
		return m.styles().dimStyle
	}
}
