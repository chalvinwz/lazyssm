package ui

import (
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/chalvinwz/lazyssm/internal/inventory"
	"github.com/chalvinwz/lazyssm/internal/preflight"
	"github.com/chalvinwz/lazyssm/internal/store"
)

func newTestModel(t *testing.T) Model {
	t.Helper()
	st, err := store.LoadFrom("") // in-memory, no disk writes
	if err != nil {
		t.Fatalf("in-memory store: %v", err)
	}
	return Model{
		store:       st,
		src:         inventory.SourceSSMOnly,
		mode:        modeList,
		searchInput: textinput.New(),
		filterInput: textinput.New(),
		width:       96,
		height:      20,
	}
}

func sampleInstances() []inventory.Instance {
	return []inventory.Instance{
		{InstanceID: "i-1", Name: "web-1", SSMReady: true, PingStatus: "Online"},
		{InstanceID: "i-2", Name: "db-1", SSMReady: false},
	}
}

func TestUpdateInstancesMsgPopulates(t *testing.T) {
	m := newTestModel(t)
	m.loading = true
	nm, _ := m.Update(instancesMsg{seq: 0, items: sampleInstances()})
	m2 := nm.(Model)
	if m2.loading {
		t.Error("loading should clear on result")
	}
	if len(m2.all) != 2 || len(m2.visible) != 2 {
		t.Fatalf("want 2 instances, got all=%d visible=%d", len(m2.all), len(m2.visible))
	}
	if m2.err != nil {
		t.Errorf("unexpected err: %v", m2.err)
	}
}

func TestUpdateStaleInstancesMsgIgnored(t *testing.T) {
	m := newTestModel(t)
	m.fetchSeq = 2
	m.all = sampleInstances()
	m.recompute()
	// A reply stamped with an older seq must not mutate state.
	nm, _ := m.Update(instancesMsg{seq: 1, items: nil})
	m2 := nm.(Model)
	if len(m2.all) != 2 {
		t.Errorf("stale reply should not clear the list, got %d", len(m2.all))
	}
}

func TestUpdatePreflightSetsBinariesOK(t *testing.T) {
	m := newTestModel(t)
	checks := []preflight.Check{
		{Name: "aws CLI", Binary: true, OK: true},
		{Name: "session-manager-plugin", Binary: true, OK: true},
		{Name: "AWS region", OK: true},
		{Name: "AWS identity", OK: true},
	}
	nm, _ := m.Update(preflightMsg{checks: checks})
	m2 := nm.(Model)
	if !m2.binariesOK {
		t.Error("binariesOK should be true when binary checks pass")
	}
	if m2.showPreflight {
		t.Error("modal should not show when all checks pass")
	}
}

func TestToggleSourceBumpsSeq(t *testing.T) {
	m := newTestModel(t)
	nm, cmd := m.keyList("t")
	m2 := nm.(Model)
	if m2.src != inventory.SourceAllEC2 {
		t.Error("source should toggle to all EC2")
	}
	if m2.fetchSeq != 1 {
		t.Errorf("fetchSeq should increment to 1, got %d", m2.fetchSeq)
	}
	if cmd == nil {
		t.Error("toggle should issue a fetch cmd")
	}
}

func TestRefreshBumpsSeq(t *testing.T) {
	m := newTestModel(t)
	nm, cmd := m.keyList("r")
	m2 := nm.(Model)
	if m2.fetchSeq != 1 || cmd == nil || !m2.loading {
		t.Errorf("refresh: seq=%d loading=%v cmd!=nil=%v", m2.fetchSeq, m2.loading, cmd != nil)
	}
}

func TestConnectGuards(t *testing.T) {
	// Empty list: no panic, no cmd.
	m := newTestModel(t)
	if _, cmd := m.connect(); cmd != nil {
		t.Error("connect on empty list should return no cmd")
	}

	// Not SSM-ready: status set, no cmd.
	m = newTestModel(t)
	m.all = sampleInstances()
	m.recompute()
	m.cursor = 1 // db-1, not ready
	nm, cmd := m.connect()
	if cmd != nil {
		t.Error("connect to non-ready instance should return no cmd")
	}
	if nm.(Model).status == "" {
		t.Error("expected a status message for non-ready connect")
	}

	// Ready but binaries not cached OK: show modal, fire preflight to populate.
	m = newTestModel(t)
	m.all = sampleInstances()
	m.recompute()
	m.cursor = 0
	m.binariesOK = false
	nm, cmd = m.connect()
	if !nm.(Model).showPreflight {
		t.Error("expected preflight modal when binaries not cached OK")
	}
	if cmd == nil {
		t.Error("expected a preflight cmd to populate the modal")
	}

	// Ready and binaries cached OK: returns a connect cmd.
	m = newTestModel(t)
	m.all = sampleInstances()
	m.recompute()
	m.cursor = 0
	m.binariesOK = true
	if _, cmd := m.connect(); cmd == nil {
		t.Error("expected a connect cmd for a ready instance with binaries OK")
	}
}

func TestTogglePinReSorts(t *testing.T) {
	m := newTestModel(t)
	m.all = sampleInstances()
	m.recompute()
	m.cursor = 1 // db-1
	id := m.visible[1].InstanceID
	nm, _ := m.togglePin()
	m2 := nm.(Model)
	if !m2.store.IsPinned(id) {
		t.Errorf("%s should be pinned", id)
	}
	if m2.visible[0].InstanceID != id {
		t.Errorf("pinned %s should sort first, got %s", id, m2.visible[0].InstanceID)
	}
}

func TestRecomputeSearchAndClamp(t *testing.T) {
	m := newTestModel(t)
	m.all = sampleInstances()
	m.searchInput.SetValue("db")
	m.cursor = 5 // out of range before recompute
	m.recompute()
	if len(m.visible) != 1 || m.visible[0].Name != "db-1" {
		t.Fatalf("search 'db' should match db-1, got %d results", len(m.visible))
	}
	if m.cursor != 0 {
		t.Errorf("cursor should clamp to 0, got %d", m.cursor)
	}

	// No match must not panic and cursor stays valid.
	m.searchInput.SetValue("zzzzz")
	m.recompute()
	if len(m.visible) != 0 || m.cursor != 0 {
		t.Errorf("no-match: visible=%d cursor=%d", len(m.visible), m.cursor)
	}
}
