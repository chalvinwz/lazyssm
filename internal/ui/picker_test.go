package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/charmbracelet/x/ansi"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/preflight"
)

func withProfiles(m Model, profiles ...awscfg.Profile) Model {
	m.listProfiles = func() ([]awscfg.Profile, error) { return profiles, nil }
	return m
}

func testProfiles() []awscfg.Profile {
	return []awscfg.Profile{
		{Name: "default", Region: "us-east-1"},
		{Name: "dev", Region: "us-west-2"},
		{Name: "prod", Region: "eu-west-1", AccountID: "111122223333"},
		{Name: "sandbox"}, // no region of its own
	}
}

func pickerNames(p picker) []string {
	out := make([]string, len(p.visible))
	for i, it := range p.visible {
		out[i] = it.name
	}
	return out
}

func TestProfilePickerOpensOnCurrent(t *testing.T) {
	m := withProfiles(newTestModel(t), testProfiles()...)
	m.profile = "prod"
	m = press(m, "P")
	if m.mode != modePicker || m.picker.kind != pickProfile {
		t.Fatalf("P should open the profile picker, mode=%v", m.mode)
	}
	if got := m.picker.visible[m.picker.cursor].name; got != "prod" {
		t.Errorf("picker should open on the current profile, got %s", got)
	}
}

func TestProfilePickerFiltersAndSwitches(t *testing.T) {
	m := withProfiles(newTestModel(t), testProfiles()...)
	m = press(m, "Pde")
	if names := pickerNames(m.picker); len(names) == 0 || names[0] != "dev" || m.picker.cursor != 0 {
		t.Fatalf("typing de should put dev first, got %v cursor=%d", names, m.picker.cursor)
	}
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	if cmd == nil || m.switchSeq != 1 || m.mode != modeList {
		t.Fatalf("Enter should close the picker and start a switch: cmd=%v seq=%d mode=%v", cmd != nil, m.switchSeq, m.mode)
	}

	m.all = sampleInstances()
	m.recompute()
	m.account = "999988887777"
	nm, cmd = m.Update(switchedMsg{seq: 1, profile: "dev", clients: awscfg.Clients{Cfg: aws.Config{Region: "us-west-2"}}})
	m = nm.(Model)
	if m.profile != "dev" || m.clients.Region() != "us-west-2" {
		t.Errorf("switch should install dev/us-west-2, got %s/%s", m.profile, m.clients.Region())
	}
	if len(m.all) != 0 || !m.loading || m.fetchSeq != 1 || m.gen != 1 || m.account != "" || cmd == nil {
		t.Errorf("switch should drop the old account's state and refetch: all=%d loading=%v seq=%d gen=%d account=%q", len(m.all), m.loading, m.fetchSeq, m.gen, m.account)
	}
	if m.store.LastProfile != "" {
		t.Errorf("a switch should not be remembered before a fetch there works, got %q", m.store.LastProfile)
	}
	nm, _ = m.Update(instancesMsg{seq: 1, items: sampleInstances()})
	m = nm.(Model)
	if m.store.LastProfile != "dev" || m.store.RegionFor("dev") != "us-west-2" {
		t.Errorf("switch should be remembered once instances load, got %q / %q", m.store.LastProfile, m.store.RegionFor("dev"))
	}
}

func TestDeniedSwitchNotRemembered(t *testing.T) {
	m := newTestModel(t)
	m.switchSeq = 1
	nm, _ := m.Update(switchedMsg{seq: 1, profile: "prod", clients: awscfg.Clients{Cfg: aws.Config{Region: "ap-southeast-2"}}})
	m = nm.(Model)
	nm, _ = m.Update(instancesMsg{seq: m.fetchSeq, err: errors.New("AccessDeniedException: not authorized")})
	m = nm.(Model)
	if m.store.LastProfile != "" || m.store.RegionFor("prod") != "" {
		t.Errorf("a region the fetch failed in should not be remembered, got %q / %q", m.store.LastProfile, m.store.RegionFor("prod"))
	}
	if m.profile != "prod" || m.clients.Region() != "ap-southeast-2" {
		t.Errorf("a denied switch should stay put, got %s / %s", m.profile, m.clients.Region())
	}
	// A later refresh that works there saves it.
	nm, _ = m.Update(instancesMsg{seq: m.fetchSeq, items: sampleInstances()})
	if st := nm.(Model).store; st.LastProfile != "prod" || st.RegionFor("prod") != "ap-southeast-2" {
		t.Errorf("a later successful fetch should save the switch, got %q / %q", st.LastProfile, st.RegionFor("prod"))
	}
}

func TestPickerEscCancels(t *testing.T) {
	m := withProfiles(newTestModel(t), testProfiles()...)
	m = press(m, "Pd")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	if m.mode != modeList || cmd != nil || m.switchSeq != 0 {
		t.Errorf("esc should close the picker without switching: mode=%v seq=%d", m.mode, m.switchSeq)
	}
}

func TestPickingCurrentProfileIsNoop(t *testing.T) {
	m := withProfiles(newTestModel(t), testProfiles()...)
	m = press(m, "P") // opens on "default", which m.profile "" means
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("choosing the profile already in use should not switch")
	}
}

func TestProfileOpensInRegion(t *testing.T) {
	m := withProfiles(newTestModel(t), testProfiles()...)
	m.clients = awscfg.Clients{Cfg: aws.Config{Region: "ap-southeast-3"}}
	if err := m.store.RememberSwitch("prod", "ap-south-1"); err != nil {
		t.Fatal(err)
	}
	m = press(m, "P")
	byName := map[string]pickerItem{}
	for _, it := range m.picker.items {
		byName[it.name] = it
	}
	for name, want := range map[string]struct{ region, note, opens, config string }{
		// Last used there. The config region differs, so it is shown too.
		"prod": {"ap-south-1", "ap-south-1", "ap-south-1 (last used)", "eu-west-1"},
		// The SDK resolves its own setting, so a config row would repeat it.
		"dev": {"", "us-west-2", "us-west-2 (aws config)", ""},
		// With nothing else, it opens in the region on screen.
		"sandbox": {"ap-southeast-3", "ap-southeast-3", "ap-southeast-3 (current region)", ""},
	} {
		it := byName[name]
		if it.region != want.region || it.note != want.note {
			t.Errorf("%s opens in region=%q note=%q, want %q / %q", name, it.region, it.note, want.region, want.note)
		}
		rows := map[string]string{}
		for _, kv := range it.detail {
			rows[kv[0]] = kv[1]
		}
		if rows["opens"] != want.opens || rows["config"] != want.config {
			t.Errorf("%s detail opens=%q config=%q, want %q / %q", name, rows["opens"], rows["config"], want.opens, want.config)
		}
	}
}

func TestRegionPickerAcceptsTypedRegion(t *testing.T) {
	m := newTestModel(t)
	m.demo = true // switch without loading real AWS config
	m = press(m, "Reu-north-9")
	if names := pickerNames(m.picker); len(names) == 0 || names[0] != "eu-north-9" {
		t.Fatalf("an unlisted region code should be offered first, got %v", names)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should switch to the typed region")
	}
	if msg, ok := cmd().(switchedMsg); !ok || msg.clients.Region() != "eu-north-9" {
		t.Errorf("want a switch to eu-north-9, got %+v", msg)
	}
}

func TestStaleSwitchIgnored(t *testing.T) {
	m := newTestModel(t)
	m.switchSeq = 2
	nm, _ := m.Update(switchedMsg{seq: 1, profile: "old"})
	if nm.(Model).profile != "" {
		t.Error("a reply from an older switch must not apply")
	}
}

func TestFailedSwitchKeepsCurrentProfile(t *testing.T) {
	m := newTestModel(t)
	m.profile = "prod"
	m.switchSeq = 1
	nm, _ := m.Update(switchedMsg{seq: 1, profile: "dev", err: errors.New("profile not found")})
	m = nm.(Model)
	if m.profile != "prod" || !strings.Contains(m.status, "switch failed") {
		t.Errorf("a failed switch should keep prod and say so, got %s / %q", m.profile, m.status)
	}
}

func TestPreflightForOldProfileIgnored(t *testing.T) {
	m := newTestModel(t)
	m.autoLogin = true
	m.gen = 1
	nm, cmd := m.Update(preflightMsg{gen: 0, checks: expiredChecks()})
	if cmd != nil || nm.(Model).preflight != nil {
		t.Error("a preflight result from before the switch must not trigger login or show")
	}
}

func TestTitleShowsAccount(t *testing.T) {
	m := newTestModel(t)
	m.profile = "prod"
	checks := append(healthyChecks(), preflight.Check{Name: "AWS identity", OK: true, Account: "111122223333"})
	nm, _ := m.Update(preflightMsg{checks: checks})
	if title := nm.(Model).titleBar(); !strings.Contains(title, "prod (111122223333)") {
		t.Errorf("title should show the profile and account, got %q", title)
	}
}

func TestRegionPickerMatchesNames(t *testing.T) {
	for query, want := range map[string]string{
		"jakarta":   "ap-southeast-3",
		"Sydney":    "ap-southeast-2",
		"frankfurt": "eu-central-1",
		"sao paulo": "sa-east-1", // São Paulo, typed without the accent
		"jkrta":     "ap-southeast-3",
		"use1":      "us-east-1", // codes still match
	} {
		m := press(newTestModel(t), "R"+query)
		if names := pickerNames(m.picker); len(names) == 0 || names[0] != want {
			t.Errorf("%q should put %s first, got %v", query, want, names)
		}
	}
}

func TestPickerMatchesEachFieldOnItsOwn(t *testing.T) {
	// "eu" can be strung together from us-east-1's code and "US East" name
	// only if the fields were matched as one string.
	m := press(newTestModel(t), "Reu")
	for _, name := range pickerNames(m.picker) {
		if name == "us-east-1" {
			t.Errorf("eu should not match us-east-1, got %v", pickerNames(m.picker))
		}
	}
}

func TestProfilePickerMatchesAccountID(t *testing.T) {
	m := press(withProfiles(newTestModel(t), testProfiles()...), "P1111")
	if names := pickerNames(m.picker); len(names) != 1 || names[0] != "prod" {
		t.Errorf("part of an account ID should find its profile, got %v", names)
	}
}

func TestRegionPickerColumns(t *testing.T) {
	m := press(newTestModel(t), "R")
	header := strings.Split(ansi.Strip(m.pickerList(60, 20)), "\n")[1]
	if fields := strings.Fields(header); len(fields) != 2 || fields[0] != "CODE" || fields[1] != "NAME" {
		t.Errorf("region columns should be CODE and NAME, got %q", header)
	}
}
