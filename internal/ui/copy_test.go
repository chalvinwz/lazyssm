package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/chalvinwz/lazyssm/internal/clipboard"
	"github.com/chalvinwz/lazyssm/internal/inventory"
)

type copyCall struct{ label, text string }

// recordCopies replaces the clipboard boundary with a recorder so tests never
// touch the real clipboard. viaTerminal is what the fake reports back.
func recordCopies(m *Model, viaTerminal bool) *[]copyCall {
	calls := &[]copyCall{}
	m.copyText = func(label, text string) (tea.Cmd, bool) {
		*calls = append(*calls, copyCall{label, text})
		return func() tea.Msg { return nil }, viaTerminal
	}
	return calls
}

func copyModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(t)
	m.all = []inventory.Instance{
		{InstanceID: "i-0a2539774a1136205", Name: "gitlab-runner", IP: "10.2.200.145"},
		{InstanceID: "i-0bbb", Name: "no-ip-box"},
	}
	m.recompute()
	return m
}

func TestCopyKeysCopyTheSelectedInstance(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want copyCall
	}{
		{"y", copyCall{"instance ID", "i-0a2539774a1136205"}},
		{"Y", copyCall{"IP", "10.2.200.145"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m := copyModel(t)
			calls := recordCopies(&m, false)
			_, cmd := m.keyList(tc.key)
			if cmd == nil {
				t.Fatal("copy must return the clipboard command")
			}
			if len(*calls) != 1 || (*calls)[0] != tc.want {
				t.Errorf("copied %+v, want exactly [%+v]", *calls, tc.want)
			}
		})
	}
}

func TestCopyIPWithoutIPCopiesNothing(t *testing.T) {
	m := copyModel(t)
	m.cursor = 1 // no-ip-box
	calls := recordCopies(&m, false)
	nm, cmd := m.keyList("Y")
	if cmd != nil || len(*calls) != 0 {
		t.Fatalf("no IP must copy nothing, got cmd=%v calls=%+v", cmd != nil, *calls)
	}
	if s := nm.(Model).status; !strings.Contains(s, "no-ip-box") || !strings.Contains(s, "no IP") {
		t.Errorf("status %q should say the instance has no IP", s)
	}
}

func TestCopyWithEmptyListIsNoop(t *testing.T) {
	m := newTestModel(t)
	calls := recordCopies(&m, false)
	if _, cmd := m.keyList("y"); cmd != nil || len(*calls) != 0 {
		t.Errorf("empty list must not copy, got cmd=%v calls=%+v", cmd != nil, *calls)
	}
}

func TestCopyViaTerminalReportsImmediately(t *testing.T) {
	m := copyModel(t)
	recordCopies(&m, true)
	nm, _ := m.keyList("y")
	if s := nm.(Model).status; !strings.Contains(s, "i-0a2539774a1136205") || !strings.Contains(s, "terminal") {
		t.Errorf("status %q should name the ID and the terminal clipboard", s)
	}
}

func TestCopiedMsgReportsOutcome(t *testing.T) {
	m := copyModel(t)
	nm, _ := m.Update(clipboard.CopiedMsg{Label: "IP", Text: "10.2.200.145"})
	if s := nm.(Model).status; !strings.Contains(s, "copied") || !strings.Contains(s, "10.2.200.145") {
		t.Errorf("success status %q should confirm the copied value", s)
	}
	nm, _ = m.Update(clipboard.CopiedMsg{Label: "IP", Text: "10.2.200.145", Err: errors.New("exit status 1")})
	if s := nm.(Model).status; !strings.Contains(s, "copy failed") || !strings.Contains(s, "exit status 1") {
		t.Errorf("failure status %q should report the error", s)
	}
}
