package ui

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/chalvinwz/lazyssm/internal/inventory"
	"github.com/chalvinwz/lazyssm/internal/preflight"
	"github.com/chalvinwz/lazyssm/internal/session"
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

// expiredChecks is a preflight result whose identity check failed on SSO expiry.
func expiredChecks() []preflight.Check {
	return []preflight.Check{
		{Name: "aws CLI", Binary: true, OK: true},
		{Name: "session-manager-plugin", Binary: true, OK: true},
		{Name: "AWS region", OK: true},
		{Name: "AWS identity", OK: false, SSOExpired: true},
	}
}

func healthyChecks() []preflight.Check {
	return []preflight.Check{
		{Name: "aws CLI", Binary: true, OK: true},
		{Name: "session-manager-plugin", Binary: true, OK: true},
		{Name: "AWS region", OK: true},
		{Name: "AWS identity", OK: true},
	}
}

// The returned tea.Cmd is only checked for nil-ness, never invoked — invoking
// it would exec `aws sso login`. Constructing it is side-effect free.

func TestAutoLoginFiresOnPreflightExpiry(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	nm, cmd := m.Update(preflightMsg{checks: expiredChecks()})
	m2 := nm.(Model)
	if cmd == nil {
		t.Fatal("expired preflight with auto-login should return a login cmd")
	}
	if !m2.loginAttempted || !m2.loginInFlight {
		t.Errorf("guard state: attempted=%v inFlight=%v, want both true", m2.loginAttempted, m2.loginInFlight)
	}
	if m2.showPreflight {
		t.Error("modal should be suppressed during the login handover")
	}
}

func TestAutoLoginSpendsOneAttempt(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	m.loginAttempted = true // attempt already spent for this expiry event
	nm, cmd := m.Update(preflightMsg{checks: expiredChecks()})
	m2 := nm.(Model)
	if cmd != nil {
		t.Fatal("second expired preflight should not fire another login")
	}
	if !m2.showPreflight {
		t.Error("spent attempt should fall back to the preflight modal")
	}
}

func TestAutoLoginGuardResetsOnHealthyPreflight(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	m.loginAttempted = true

	nm, _ := m.Update(preflightMsg{checks: healthyChecks()})
	m2 := nm.(Model)
	if m2.loginAttempted {
		t.Fatal("healthy preflight should reset the attempt guard")
	}

	// A later expiry gets a fresh attempt.
	nm2, cmd := m2.Update(preflightMsg{checks: expiredChecks()})
	if cmd == nil {
		t.Error("a later expiry after recovery should fire auto-login again")
	}
	if !nm2.(Model).loginAttempted {
		t.Error("fresh attempt should be spent")
	}
}

func TestAutoLoginFiresOnFetchSSOError(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	nm, cmd := m.Update(instancesMsg{seq: 0, err: errors.New("the SSO session associated with this profile has expired")})
	m2 := nm.(Model)
	if cmd == nil {
		t.Fatal("SSO-expired fetch error should fire auto-login")
	}
	if !m2.loginAttempted || !m2.loginInFlight {
		t.Errorf("guard state: attempted=%v inFlight=%v, want both true", m2.loginAttempted, m2.loginInFlight)
	}

	// Generic fetch errors must not trigger a login.
	m = newTestModel(t)
	m.autoLogin = true
	if _, cmd := m.Update(instancesMsg{seq: 0, err: errors.New("RequestLimitExceeded")}); cmd != nil {
		t.Error("generic fetch error should not fire auto-login")
	}
}

func TestAutoLoginDisabledOrDemoNeverFires(t *testing.T) {
	m := newTestModel(t) // autoLogin false
	if _, cmd := m.Update(preflightMsg{checks: expiredChecks()}); cmd != nil {
		t.Error("auto-login disabled: no cmd expected")
	}

	m = newTestModel(t)
	m.autoLogin = true
	m.demo = true
	if _, cmd := m.Update(preflightMsg{checks: expiredChecks()}); cmd != nil {
		t.Error("demo mode: no cmd expected")
	}
}

func TestLoginEndedRerunsPreflightAndFetch(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	m.loginAttempted = true
	m.loginInFlight = true
	nm, cmd := m.Update(session.LoginEndedMsg{})
	m2 := nm.(Model)
	if m2.loginInFlight {
		t.Error("loginInFlight should clear when the login exits")
	}
	if !m2.loginAttempted {
		t.Error("the attempt stays spent until identity recovers")
	}
	if m2.fetchSeq != 1 || !m2.loading || cmd == nil {
		t.Errorf("re-verify: seq=%d loading=%v cmd!=nil=%v", m2.fetchSeq, m2.loading, cmd != nil)
	}
}

func TestStalePreflightIgnoredWhileLoginInFlight(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	m.loginAttempted = true
	m.loginInFlight = true
	nm, cmd := m.Update(preflightMsg{checks: expiredChecks()})
	m2 := nm.(Model)
	if cmd != nil {
		t.Error("no cmd while a login is suspended")
	}
	if m2.showPreflight {
		t.Error("stale expired preflight should not open the modal over the login")
	}
}

func TestFetchSuccessResetsLoginAttempt(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	m.loginAttempted = true
	nm, _ := m.Update(instancesMsg{seq: 0, items: sampleInstances()})
	if nm.(Model).loginAttempted {
		t.Error("a successful fetch proves the expiry event is over; guard should reset")
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
