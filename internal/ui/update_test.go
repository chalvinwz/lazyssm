package ui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
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
		pickerInput: textinput.New(),
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
	nm, cmd := m.keyList(keyPress("t"))
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
	nm, cmd := m.keyList(keyPress("r"))
	m2 := nm.(Model)
	if m2.fetchSeq != 1 || cmd == nil || !m2.loading {
		t.Errorf("refresh: seq=%d loading=%v cmd!=nil=%v", m2.fetchSeq, m2.loading, cmd != nil)
	}
}

func TestConnectGuards(t *testing.T) {
	// An empty list returns no cmd and does not panic.
	m := newTestModel(t)
	if _, cmd := m.connect(); cmd != nil {
		t.Error("connect on empty list should return no cmd")
	}

	// An instance that is not SSM-ready sets a status and returns no cmd.
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

	// Without binaries cached OK, connect opens the modal and runs preflight.
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

	// With binaries cached OK, connect returns a session cmd.
	m = newTestModel(t)
	m.all = sampleInstances()
	m.recompute()
	m.cursor = 0
	m.binariesOK = true
	m.startSession = func(_, _, _ string) tea.Cmd { return func() tea.Msg { return nil } }
	if _, cmd := m.connect(); cmd == nil {
		t.Error("expected a connect cmd for a ready instance with binaries OK")
	}
}

func TestConnectUsesResolvedRegion(t *testing.T) {
	m := newTestModel(t)
	// m.region is empty without --region; the SDK resolved it from the profile.
	m.clients = awscfg.Clients{Cfg: aws.Config{Region: "eu-west-1"}}
	m.all = sampleInstances()
	m.recompute()
	m.binariesOK = true
	var gotRegion string
	m.startSession = func(_, _, region string) tea.Cmd {
		gotRegion = region
		return nil
	}
	m.connect()
	if gotRegion != "eu-west-1" {
		t.Errorf("session should open in the browsed region eu-west-1, got %q", gotRegion)
	}
}

// manyInstances is a sorted list long enough for the cursor to sit below the
// rows a search ranks first.
func manyInstances() []inventory.Instance {
	var out []inventory.Instance
	for _, n := range []string{"api-1", "api-2", "cache-1", "db-1", "db-2", "web-a", "web-b", "web-c", "worker-1", "worker-2"} {
		out = append(out, inventory.Instance{InstanceID: "i-" + n, Name: n, SSMReady: true})
	}
	return out
}

func loaded(t *testing.T, items []inventory.Instance) Model {
	t.Helper()
	nm, _ := newTestModel(t).Update(instancesMsg{items: items})
	return nm.(Model)
}

// keyPress is the message for typing the single character s.
func keyPress(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func press(m Model, typed string) Model {
	for _, r := range typed {
		nm, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = nm.(Model)
	}
	return m
}

func TestSearchStartsOnBestMatch(t *testing.T) {
	m := loaded(t, manyInstances())
	m.cursor = 7 // left on web-c while browsing the full list
	m = press(m, "/w")
	if m.cursor != 0 {
		t.Fatalf("a new query should start on the best match, cursor=%d", m.cursor)
	}

	best := m.visible[0].InstanceID
	var connected string
	m.binariesOK = true
	m.startSession = func(id, _, _ string) tea.Cmd {
		connected = id
		return nil
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if connected != best {
		t.Errorf("Enter should connect to the top match %s, connected to %q", best, connected)
	}
}

func TestPinKeepsSelection(t *testing.T) {
	m := loaded(t, manyInstances())
	m.cursor = 8 // worker-1
	m = press(m, "p")
	if got := m.selectedID(); got != "i-worker-1" {
		t.Errorf("after pinning, selection should stay on i-worker-1, got %s", got)
	}
	if m.cursor != 0 {
		t.Errorf("the pinned instance sorts first, so the cursor should follow it to 0, got %d", m.cursor)
	}
	m = press(m, "p")
	if got := m.selectedID(); got != "i-worker-1" {
		t.Errorf("after unpinning, selection should stay on i-worker-1, got %s", got)
	}
}

func TestRefreshKeepsSelection(t *testing.T) {
	m := loaded(t, manyInstances())
	m.cursor = 3 // db-1
	// The refresh brings in an instance that sorts above db-1.
	items := append(manyInstances(), inventory.Instance{InstanceID: "i-bastion", Name: "bastion", SSMReady: true})
	nm, _ := m.Update(instancesMsg{items: items})
	if got := nm.(Model).selectedID(); got != "i-db-1" {
		t.Errorf("refresh should keep i-db-1 selected, got %s", got)
	}
}

func TestFetchWarningShownInStatus(t *testing.T) {
	nm, _ := newTestModel(t).Update(instancesMsg{items: sampleInstances(), warning: "no ec2 access"})
	if status := nm.(Model).status; !strings.Contains(status, "no ec2 access") {
		t.Errorf("status should carry the fetch warning, got %q", status)
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
	if m2.cursor != 0 {
		t.Errorf("selection should follow the pinned instance to the top, cursor=%d", m2.cursor)
	}
}

// expiredChecks is a preflight result whose identity check failed because the
// SSO session expired.
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

// The auto-login tests check the returned tea.Cmd only for nil and never run
// it, since it would exec `aws sso login`. Building it has no side effects.

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
	if m2.showPreflight {
		t.Error("a spent attempt should leave the expiry to the list panel, not the checklist")
	}
	if nm, _ = m2.Update(instancesMsg{err: errors.New("the SSO session associated with this profile has expired")}); !nm.(Model).needsSignIn() {
		t.Error("the list should offer L to sign in")
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

	// No match must not panic, and the cursor stays valid.
	m.searchInput.SetValue("zzzzz")
	m.recompute()
	if len(m.visible) != 0 || m.cursor != 0 {
		t.Errorf("no-match: visible=%d cursor=%d", len(m.visible), m.cursor)
	}
}
