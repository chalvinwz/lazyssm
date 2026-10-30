package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// allActions lists every action in keyMap, by field name.
func allActions() map[string]action {
	out := map[string]action{}
	v := reflect.ValueOf(keys)
	for i := range v.NumField() {
		out[v.Type().Field(i).Name] = v.Field(i).Interface().(action)
	}
	return out
}

func TestEveryKeyIsInTheHelp(t *testing.T) {
	listed := map[string]bool{}
	for _, a := range helpOrder {
		listed[a.Help().Key+" "+a.about] = true
	}
	for name, a := range allActions() {
		if name == "Move" { // a footer-only hint for Up and Down
			continue
		}
		if a.about == "" || !listed[a.Help().Key+" "+a.about] {
			t.Errorf("%s (%v) is missing from the ? overlay", name, a.Keys())
		}
	}
}

func TestNoKeyIsBoundTwice(t *testing.T) {
	owner := map[string]string{}
	for name, a := range allActions() {
		if name == "Move" {
			continue
		}
		for _, k := range a.Keys() {
			if other, ok := owner[k]; ok {
				t.Errorf("%q is bound to both %s and %s", k, other, name)
			}
			owner[k] = name
		}
	}
}

func TestHelpOverlayListsKeys(t *testing.T) {
	m := newTestModel(t)
	help := ansi.Strip(m.renderHelp())
	if lines := strings.Count(help, "\n") + 1; lines > 24 {
		t.Errorf("the ? overlay should fit a 24-line terminal, got %d lines", lines)
	}
	for _, want := range []string{"j / ↓", "move down", "q / ctrl+c", "L", "sign in to AWS SSO (aws sso login)", "esc", "close the error panel"} {
		if !strings.Contains(help, want) {
			t.Errorf("the ? overlay should show %q:\n%s", want, help)
		}
	}
}

func TestFooterFits(t *testing.T) {
	if got := fitFooter(footerKeys, 200); len(got) != len(footerKeys) {
		t.Errorf("a wide terminal should show every hint, got %d of %d", len(got), len(footerKeys))
	}
	got := fitFooter(footerKeys, 96)
	if footerWidth(got) > 96 {
		t.Errorf("hints overflow 96 columns: %d", footerWidth(got))
	}
	shown := map[string]bool{}
	for _, b := range got {
		shown[b.Help().Key] = true
	}
	for _, keep := range []string{"P", "R", "⏎", "?"} {
		if !shown[keep] {
			t.Errorf("%q should survive at 96 columns", keep)
		}
	}
	m := newTestModel(t)
	m.width = 96
	if footer := ansi.Strip(m.footerHints()); !strings.Contains(footer, "P profile · R region · ⏎ connect · ? help · q quit") {
		t.Errorf("footer = %q", footer)
	}
}
