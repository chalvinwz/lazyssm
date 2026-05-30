package ui

import (
	"os"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

// TestRenderSnapshot prints a frame for manual inspection: go test -run Snapshot -v
func TestRenderSnapshot(t *testing.T) {
	all := []inventory.Instance{
		{Name: "Group7-app-public", InstanceID: "i-07a1b2c3", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.4.12", PlatformType: "Linux", AgentStatus: "Latest", Pinned: true, Tags: map[string]string{"Env": "prod", "Team": "g7"}},
		{Name: "Group7-app-private", InstanceID: "i-0b72aa01", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.4.13", PlatformType: "Linux"},
		{Name: "Group8-twitter", InstanceID: "i-0e7f1234", SSMReady: true, PingStatus: "Online", State: "running", IP: "10.0.5.20", PlatformType: "Linux"},
		{Name: "Group10-prod-stateful", InstanceID: "i-07c9aa55", SSMReady: false, PingStatus: "ConnectionLost", State: "stopped"},
		{Name: "Group1-url-shortener", InstanceID: "i-0777aaaa", SSMReady: true, PingStatus: "Online", IP: "10.0.1.9", PlatformType: "Linux"},
	}
	si := textinput.New()
	m := Model{
		profile: "institute", region: "ap-southeast-3",
		all: all, visible: all, cursor: 0,
		src: inventory.SourceSSMOnly, mode: modeList,
		searchInput: si, filterInput: textinput.New(),
		width: 96, height: 20, status: "5 instances",
	}
	if out := os.Getenv("SNAP_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(m.render()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Smoke: render must not panic and must produce output across sizes,
	// including the compact fallback and the empty/help/search states.
	sizes := [][2]int{{96, 20}, {120, 40}, {60, 12}, {30, 8}, {200, 50}}
	for _, sz := range sizes {
		mm := m
		mm.width, mm.height = sz[0], sz[1]
		if mm.render() == "" {
			t.Errorf("empty render at %dx%d", sz[0], sz[1])
		}
		mm.mode = modeHelp
		_ = mm.render()
		mm.mode = modeList
		mm.visible = nil
		if mm.render() == "" {
			t.Errorf("empty render (no instances) at %dx%d", sz[0], sz[1])
		}
	}
}

// TestRenderEmptyListShowsPrompt asserts the empty-list content (not just that
// the frame is non-empty): with no instances the list panel guides the user.
func TestRenderEmptyListShowsPrompt(t *testing.T) {
	m := newTestModel(t) // 96x20 -> renderSplit; no instances loaded
	out := m.render()
	if !strings.Contains(out, "no instances") {
		t.Fatalf("empty list should prompt to filter/toggle source, got:\n%s", out)
	}
}
