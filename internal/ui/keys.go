package ui

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// action is one thing the instance list does: the keys that trigger it with
// their footer label (the binding's help), and its line in the ? overlay.
type action struct {
	key.Binding
	about string
}

func newAction(keys []string, short, label, about string) action {
	return action{key.NewBinding(key.WithKeys(keys...), key.WithHelp(short, label)), about}
}

// keyMap is every key the instance list answers to. Matching keys, the footer
// hints and the ? overlay all read it, so a key is defined in one place.
type keyMap struct {
	Down, Up, Top, Bottom           action
	Filter, Search, Toggle, Refresh action
	Connect, Pin, CopyID, CopyIP    action
	Profile, Region, SignIn         action
	Error, CloseError, Help, Quit   action
	Move                            action // the footer's one hint for Up and Down
}

var keys = keyMap{
	Down:       newAction([]string{"j", "down"}, "↓", "down", "move down"),
	Up:         newAction([]string{"k", "up"}, "↑", "up", "move up"),
	Top:        newAction([]string{"g", "home"}, "g", "top", "jump to the top"),
	Bottom:     newAction([]string{"G", "end"}, "G", "bottom", "jump to the bottom"),
	Filter:     newAction([]string{"f"}, "f", "filter", "filter by tag or Name prefix (server-side)"),
	Search:     newAction([]string{"/"}, "/", "search", "fuzzy search (↑↓ to move, ⏎ connects)"),
	Toggle:     newAction([]string{"t"}, "t", "toggle", "toggle SSM-only ↔ all EC2"),
	Refresh:    newAction([]string{"r"}, "r", "refresh", "refresh the inventory"),
	Connect:    newAction([]string{"enter"}, "⏎", "connect", "connect (SSM shell)"),
	Pin:        newAction([]string{"p"}, "p", "pin", "pin / unpin the selected instance"),
	CopyID:     newAction([]string{"y"}, "y", "copy ID", "copy the instance ID (or the error, while e shows it)"),
	CopyIP:     newAction([]string{"Y"}, "Y", "copy IP", "copy the IP"),
	Profile:    newAction([]string{"P"}, "P", "profile", "switch AWS profile"),
	Region:     newAction([]string{"R"}, "R", "region", "switch AWS region"),
	SignIn:     newAction([]string{"L"}, "L", "sign in", "sign in to AWS SSO (aws sso login)"),
	Error:      newAction([]string{"e"}, "e", "error", "show / hide the last AWS error"),
	CloseError: newAction([]string{"esc"}, "esc", "close", "close the error panel"),
	Help:       newAction([]string{"?"}, "?", "help", "toggle this help"),
	Quit:       newAction([]string{"q", "ctrl+c"}, "q", "quit", "quit"),
	Move:       newAction([]string{"up", "down"}, "↑↓", "move", ""),
}

// footerKeys are the hints under the list, in order. On a narrow terminal the
// ones in footerDropOrder go first; the ? overlay still lists every key.
var (
	footerKeys      = []action{keys.Move, keys.Filter, keys.Search, keys.Toggle, keys.Pin, keys.Refresh, keys.Profile, keys.Region, keys.Connect, keys.Help, keys.Quit}
	footerDropOrder = []action{keys.Toggle, keys.Refresh, keys.Filter, keys.Pin, keys.Search, keys.Region, keys.Profile}
)

// helpOrder is the ? overlay, top to bottom: moving, finding, the selected
// instance, the account, then lazyssm itself. One line each, so it fits a
// 24-line terminal.
var helpOrder = []action{
	keys.Down, keys.Up, keys.Top, keys.Bottom,
	keys.Filter, keys.Search, keys.Toggle, keys.Refresh,
	keys.Connect, keys.Pin, keys.CopyID, keys.CopyIP,
	keys.Profile, keys.Region, keys.SignIn, keys.Error, keys.CloseError,
	keys.Help, keys.Quit,
}

// footerSeparator sits between footer hints.
const footerSeparator = " · "

// fitFooter drops hints in footerDropOrder until the rest fit in width.
func fitFooter(hints []action, width int) []key.Binding {
	out := make([]key.Binding, 0, len(hints))
	for _, a := range hints {
		out = append(out, a.Binding)
	}
	for _, drop := range footerDropOrder {
		if width <= 0 || footerWidth(out) <= width {
			break
		}
		kept := out[:0:0]
		for _, b := range out {
			if b.Help() != drop.Help() {
				kept = append(kept, b)
			}
		}
		out = kept
	}
	return out
}

func footerWidth(bindings []key.Binding) int {
	w := lipgloss.Width(footerSeparator) * max(0, len(bindings)-1)
	for _, b := range bindings {
		w += lipgloss.Width(b.Help().Key) + 1 + lipgloss.Width(b.Help().Desc)
	}
	return w
}

// helpModel renders key hints in the current theme.
func (m Model) helpModel() help.Model {
	s := m.styles()
	h := help.New()
	h.ShortSeparator = footerSeparator
	h.Styles = help.Styles{
		Ellipsis:       s.faintStyle,
		ShortKey:       s.keyStyle,
		ShortDesc:      s.faintStyle,
		ShortSeparator: s.faintStyle,
		FullKey:        s.keyStyle,
		FullDesc:       s.detailValue,
		FullSeparator:  s.faintStyle,
	}
	h.SetWidth(m.width)
	return h
}

func (m Model) footerHints() string {
	return m.helpModel().ShortHelpView(fitFooter(footerKeys, m.width))
}

// overlayBinding is a's line in the ? overlay: every key that triggers it,
// then its description.
func overlayBinding(a action) key.Binding {
	names := make([]string, 0, len(a.Keys()))
	for _, k := range a.Keys() {
		switch k {
		case "up":
			k = "↑"
		case "down":
			k = "↓"
		}
		names = append(names, k)
	}
	b := a.Binding
	b.SetHelp(strings.Join(names, " / "), a.about)
	return b
}

func (m Model) renderHelp() string {
	s := m.styles()
	h := m.helpModel()
	h.SetWidth(0) // one column; nothing to cut
	bindings := make([]key.Binding, 0, len(helpOrder))
	for _, a := range helpOrder {
		bindings = append(bindings, overlayBinding(a))
	}
	var b strings.Builder
	b.WriteString(s.titleStyle.Render("lazyssm — keybindings"))
	b.WriteString("\n\n")
	for _, line := range strings.Split(h.FullHelpView([][]key.Binding{bindings}), "\n") {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n" + s.dimStyle.Render("press any key to return"))
	return b.String()
}
