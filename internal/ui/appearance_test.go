package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/inventory"
	"github.com/chalvinwz/lazyssm/internal/store"
	"github.com/charmbracelet/x/ansi"
)

func TestTransparentBackgroundWithThemeOverride(t *testing.T) {
	st := &store.Store{Theme: "dracula", TransparentBackground: true}
	m := New(awscfg.Clients{}, st, "", "", false).WithTheme("nord")
	s := m.styles()
	if m.View().BackgroundColor != nil || s.panelStyle.GetBackground() != (lipgloss.NoColor{}) || s.panelStyle.GetBorderTopBackground() != (lipgloss.NoColor{}) || s.nameStyle.GetBackground() != (lipgloss.NoColor{}) {
		t.Fatal("transparent mode retained a theme background")
	}
	if s.name != "nord" || s.selStyle.GetBackground() == (lipgloss.NoColor{}) || s.nameStyle.GetForeground() == (lipgloss.NoColor{}) || st.Theme != "dracula" {
		t.Fatal("transparent mode changed theme colors, selection, or configuration")
	}
	opaque := New(awscfg.Clients{}, &store.Store{Theme: "dracula"}, "", "", false)
	if opaque.View().BackgroundColor == nil || opaque.styles().panelStyle.GetBorderTopBackground() == (lipgloss.NoColor{}) {
		t.Fatal("default theme background was removed")
	}
}

func TestInstanceAge(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		launch time.Time
		want   string
	}{
		{"unknown", time.Time{}, "—"},
		{"future", now.Add(time.Hour), "0m"},
		{"seconds", now.Add(-30 * time.Second), "0m"},
		{"minutes", now.Add(-59 * time.Minute), "59m"},
		{"hours", now.Add(-25 * time.Hour), "1d 1h"},
		{"hour boundary", now.Add(-time.Hour), "1h 0m"},
		{"days", now.Add(-2400 * time.Hour), "100d 0h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceAge(tc.launch, now); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAppearanceContentAndWidths(t *testing.T) {
	m := newTestModel(t)
	m.loading = false
	m.store.Columns = []string{"name", "age", "state", "ssm"}
	m.visible = []inventory.Instance{{Name: "web-prod", InstanceID: "i-test", State: "shutting-down", SSMReady: false, LaunchTime: time.Now().Add(-49 * time.Hour)}}
	for _, w := range []int{34, 48, 74, 120} {
		line := m.rowLine(0, m.visible[0], w, m.listColumns(w))
		if got := lipgloss.Width(line); got > w {
			t.Errorf("row width %d exceeds %d", got, w)
		}
		if w >= 48 && (!strings.Contains(line, "shutting-down") || !strings.Contains(line, "2d 1h")) {
			t.Errorf("missing state/age: %q", line)
		}
	}
	for _, content := range []string{m.listContent(74, 20), m.detailContent(42, 20)} {
		if !strings.Contains(content, "shutting-down") || !strings.Contains(content, "2d 1h") {
			t.Errorf("missing state/age: %q", content)
		}
	}
}

func TestThemePresetsAndIsolation(t *testing.T) {
	for _, name := range []string{"default", "tokyo-night", "dracula", "nord", "catppuccin-mocha"} {
		st := &store.Store{Theme: name}
		m := New(awscfg.Clients{}, st, "", "", false)
		if m.styles().name != name {
			t.Errorf("theme %q resolved to %q", name, m.styles().name)
		}
	}
	a := New(awscfg.Clients{}, &store.Store{Theme: "dracula"}, "", "", false)
	b := New(awscfg.Clients{}, &store.Store{Theme: "nord"}, "", "", false)
	if a.styles().name != "dracula" || b.styles().name != "nord" {
		t.Fatal("themes leaked between models")
	}
	if got := New(awscfg.Clients{}, &store.Store{Theme: "typo"}, "", "", false).styles().name; got != "default" {
		t.Fatalf("unknown theme = %q", got)
	}
}

func TestResponsiveIPColumn(t *testing.T) {
	m := newTestModel(t)
	m.store.Columns = []string{"name", "ip", "state", "age", "ssm"}
	m.visible = []inventory.Instance{{Name: "web", IP: "10.1.2.3", State: "running"}}
	for _, w := range []int{34, 38, 39, 48, 62, 63, 120} {
		got := m.listContent(w, 20)
		if strings.Contains(got, "10.1.2.3") != (w >= 39) {
			t.Errorf("IP visibility at %d: %q", w, got)
		}
		for _, line := range strings.Split(got, "\n") {
			if lipgloss.Width(line) > w {
				t.Errorf("line exceeds %d: %q", w, line)
			}
		}
	}
}

func TestDefaultPaletteAndThemeOverride(t *testing.T) {
	s := makeStyles("", false)
	if s.name != "default" || s.titleStyle.GetBackground() != lipgloss.Color("#FF9900") || s.nameStyle.GetForeground() != lipgloss.Color("#E6EDF3") {
		t.Fatal("default palette changed")
	}
	st := &store.Store{Theme: "dracula", AutoLogin: true, Pinned: []string{"i-pinned"}}
	m := New(awscfg.Clients{}, st, "", "", true).WithTheme("nord")
	if m.styles().name != "nord" || st.Theme != "dracula" || !m.autoLogin || len(st.Pinned) != 1 {
		t.Fatal("theme override changed persisted settings")
	}
}

func TestDemoFrames(t *testing.T) {
	for _, theme := range []string{"default", "tokyo-night", "dracula", "nord", "catppuccin-mocha"} {
		m := NewDemo("", "").WithTheme(theme)
		m.visible = demoInstances()
		m.loading = false
		for _, size := range [][2]int{{60, 20}, {96, 24}, {120, 24}, {40, 12}} {
			m.width, m.height = size[0], size[1]
			frame := m.render()
			if size[0] >= 96 && !strings.Contains(frame, "2d 1h") {
				t.Errorf("missing age for %s at %d", theme, size[0])
			}
			if size[0] >= 60 {
				lines := strings.Split(frame, "\n")
				// Check the panel body apart from the wrapped title and footer.
				for _, line := range lines[1 : len(lines)-2] {
					if lipgloss.Width(line) > size[0] {
						t.Errorf("body overflow for %s at %d: %q", theme, size[0], line)
					}
				}
			}
		}
	}
}

func TestStateColorsAndThemeValidation(t *testing.T) {
	m := NewDemo("", "")
	for state, want := range map[string]string{"running": "#3FB950", "pending": "#F0B72F", "stopping": "#F0B72F", "shutting-down": "#F0B72F", "stopped": "#F85149", "terminated": "#F85149", "": "#8B949E", "unknown": "#8B949E"} {
		if got := m.stateStyle(state).GetForeground(); got != lipgloss.Color(want) {
			t.Errorf("state %q: got %v, want %s", state, got, want)
		}
	}
	for _, name := range []string{"default", "tokyo-night", "dracula", "nord", "catppuccin-mocha"} {
		if !ValidTheme(name) {
			t.Errorf("rejected %s", name)
		}
	}
	for _, name := range []string{"", "typo", "Dracula"} {
		if ValidTheme(name) {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestAllListWidths(t *testing.T) {
	m := NewDemo("", "")
	m.visible = demoInstances()
	m.loading = false
	for _, columns := range [][]string{nil, {"name", "ip", "state", "age", "ssm"}, {"id", "name", "os", "agent", "ping", "ssm"}} {
		m.store.Columns = columns
		for w := 23; w <= 200; w++ {
			for _, line := range strings.Split(m.listContent(w, 20), "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("list width %d got %d: %q", w, got, line)
				}
			}
		}
	}
}

func TestAllTerminalWidths(t *testing.T) {
	m := NewDemo("", "").WithTheme("dracula")
	m.visible = demoInstances()
	m.loading = false
	m.height = 25
	for w := 60; w <= 200; w++ {
		m.width = w
		for _, line := range strings.Split(m.render(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Errorf("terminal width %d got %d: %q", w, got, line)
			}
		}
	}
}

func TestSplitPanelDimensions(t *testing.T) {
	m := NewDemo("", "").WithTheme("dracula")
	m.visible = demoInstances()
	m.loading = false
	for _, w := range []int{60, 96, 120, 130, 160} {
		m.width = w
		m.height = 25
		frame := m.render()
		if got := lipgloss.Height(frame); got != m.height {
			t.Errorf("height at %d = %d, want %d", w, got, m.height)
		}
		if got := lipgloss.Width(frame); got != w {
			t.Errorf("width = %d, want %d", got, w)
		}
		for _, line := range strings.Split(frame, "\n") {
			if strings.Contains(line, "SSM") && !strings.Contains(line, "NAME") && !strings.Contains(line, "demo") {
				t.Errorf("SSM header wrapped at %d: %q", w, line)
			}
		}
	}
}

func TestColumnConfigurationAndNarrowDefaults(t *testing.T) {
	m := newTestModel(t)
	m.visible = []inventory.Instance{{Name: "web-prod-12345678", IP: "10.1.2.3", State: "running"}}
	for w := 23; w <= 80; w++ {
		columns := m.listColumns(w)
		if columns[0].name != "name" || columns[0].width < 16 {
			t.Fatalf("name squeezed at %d: %+v", w, columns)
		}
		for _, column := range columns {
			if column.name == "ip" || column.name == "state" {
				t.Fatalf("optional column visible by default: %+v", columns)
			}
			if column.name == "age" && w < 33 {
				t.Fatalf("age visible without name space at %d", w)
			}
		}
	}
	m.store.Columns = []string{"ip", "name", "state", "age", "ssm", "ip", "typo", "id", "os", "agent", "ping"}
	columns := m.listColumns(200)
	var names []string
	for _, column := range columns {
		names = append(names, column.name)
	}
	if got := strings.Join(names, ","); got != "ip,name,state,age,ssm,id,os,agent,ping" {
		t.Fatalf("column order = %s", got)
	}
	header := ansi.Strip(m.listContent(200, 20))
	if strings.Index(header, "IP") > strings.Index(header, "NAME") {
		t.Fatal("configured order ignored")
	}
	m.store.Columns = []string{"typo"}
	if got := m.listColumns(80); len(got) != 3 || got[0].name != "name" {
		t.Fatalf("invalid columns fallback: %+v", got)
	}
	m.store.Columns = []string{"state"}
	if got := m.listColumns(80); len(got) != 2 || got[0].name != "name" {
		t.Fatalf("missing name fallback: %+v", got)
	}
}

func TestNamesTakePriorityOverOtherColumns(t *testing.T) {
	m := newTestModel(t)
	m.store.Columns = []string{"name", "ip", "state", "age", "ssm"}
	m.visible = []inventory.Instance{
		{Name: "short", IP: "10.0.0.1", State: "running"},
		{Name: "web-production-eu-west-1-blue", IP: "10.0.0.2", State: "running"},
	}
	for w := 3 + lipgloss.Width(m.visible[1].Name); w <= 100; w++ {
		columns := m.listColumns(w)
		for i, in := range m.visible {
			row := ansi.Strip(m.rowLine(i, in, w, columns))
			if !strings.Contains(row, in.Name) || lipgloss.Width(row) > w {
				t.Fatalf("name clipped or row overflowed at %d: %q", w, row)
			}
		}
	}
	columns := m.listColumns(24)
	if len(columns) != 1 || columns[0].name != "name" {
		t.Fatalf("other columns remained while the name did not fit: %+v", columns)
	}
	row := m.rowLine(1, m.visible[1], 24, columns)
	if lipgloss.Width(row) > 24 {
		t.Fatalf("name overflowed the panel: %q", row)
	}
}

func TestSelectedRowHasOneHighlight(t *testing.T) {
	for _, transparent := range []bool{false, true} {
		st := &store.Store{Theme: "dracula", TransparentBackground: transparent, Columns: []string{"name", "state", "ssm", "ip", "age"}}
		m := New(awscfg.Clients{}, st, "", "", false)
		in := inventory.Instance{Name: "web-prod", State: "running", SSMReady: true, IP: "10.0.0.1", Pinned: true}
		row := m.rowLine(0, in, 100, m.listColumns(100))
		want := m.styles().selStyle.Width(100).Render(ansi.Strip(row))
		if row != want {
			t.Fatalf("selected row contains nested styles (transparent=%v): %q", transparent, row)
		}
		if !strings.Contains(ansi.Strip(row), "running") || !strings.Contains(ansi.Strip(row), "●") {
			t.Fatal("selected row lost status")
		}
	}
}

func TestListKeepsRightPadding(t *testing.T) {
	m := newTestModel(t)
	m.visible = []inventory.Instance{{Name: "web", SSMReady: true}}
	header := strings.Split(ansi.Strip(m.listContent(80, 20)), "\n")[1]
	if !strings.HasSuffix(header, "SSM") || lipgloss.Width(header) != 80-listRightPad {
		t.Errorf("the last heading should end %d cells before the border, got %d wide: %q", listRightPad, lipgloss.Width(header), header)
	}
	if row := m.rowLine(0, m.visible[0], 80, m.listColumns(80)); lipgloss.Width(row) != 80 {
		t.Errorf("the selected row's highlight should still span the panel, got %d", lipgloss.Width(row))
	}
	// With no room to spare, the padding goes before the name is cut.
	name := "web-production-eu-west-1-blue"
	m.visible = []inventory.Instance{{Name: name}}
	if row := ansi.Strip(m.rowLine(0, m.visible[0], 3+len(name), m.listColumns(3+len(name)))); !strings.Contains(row, name) {
		t.Errorf("padding should never clip a name: %q", row)
	}
}
