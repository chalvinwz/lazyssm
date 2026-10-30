package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTogglePersistsAcrossLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.IsPinned("i-1") {
		t.Fatal("fresh store should have no pins")
	}

	on, err := s.TogglePin("i-1")
	if err != nil {
		t.Fatal(err)
	}
	if !on || !s.IsPinned("i-1") {
		t.Fatal("toggle should pin i-1")
	}

	// Reload from disk; pin must survive.
	s2, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsPinned("i-1") {
		t.Error("pin did not persist across reload")
	}

	// Toggle off.
	if on, _ := s2.TogglePin("i-1"); on {
		t.Error("second toggle should unpin")
	}
	s3, _ := LoadFrom(path)
	if s3.IsPinned("i-1") {
		t.Error("unpin did not persist")
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s, err := LoadFrom(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(s.Pinned) != 0 {
		t.Error("missing file should yield empty store")
	}
}

func TestSavesNeverRewriteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := "# my lazyssm settings\ntheme: dracula # favourite\ntransparent_background: true\ncolumns: [name, ip, state, age, ssm]\nauto_login: true\nfuture_key: keep-me\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TogglePin("i-new"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberSwitch("prod", "eu-west-1"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != config {
		t.Errorf("config.yaml changed, comments and unknown keys must survive:\n%s", data)
	}
	saved, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Columns) != 5 || saved.Columns[1] != "ip" || saved.Theme != "dracula" || !saved.TransparentBackground || !saved.AutoLogin || !saved.IsPinned("i-new") {
		t.Fatalf("settings or pins lost: %+v", saved)
	}
}

func TestStateHoldsOnlyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("theme: nord\nauto_login: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TogglePin("i-1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"theme", "auto_login", "last_profile"} {
		if strings.Contains(string(data), key) {
			t.Errorf("state.yaml should not contain %s, got:\n%s", key, data)
		}
	}
	if !strings.Contains(string(data), "i-1") {
		t.Errorf("state.yaml should hold the pin, got:\n%s", data)
	}
}

func TestMigratesLegacyPins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	legacy := "theme: dracula\npinned:\n  - i-1\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsPinned("i-1") {
		t.Fatal("pins from config.yaml should carry over while state.yaml doesn't exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.yaml")); !os.IsNotExist(err) {
		t.Error("loading alone must not write state.yaml")
	}

	if _, err := s.TogglePin("i-2"); err != nil {
		t.Fatal(err)
	}
	s2, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsPinned("i-1") || !s2.IsPinned("i-2") {
		t.Fatalf("both the migrated and the new pin should be saved, got %v", s2.Pinned)
	}

	// Once state.yaml exists, the old key in config.yaml is ignored.
	if _, err := s2.TogglePin("i-1"); err != nil {
		t.Fatal(err)
	}
	s3, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s3.IsPinned("i-1") {
		t.Error("an unpin must stick even though config.yaml still lists the pin")
	}
	if data, _ := os.ReadFile(path); string(data) != legacy {
		t.Errorf("migration must leave config.yaml untouched, got:\n%s", data)
	}
}

func TestRememberSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RememberSwitch("prod", "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberSwitch("dev", "ap-southeast-3"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberSwitch("", "us-east-1"); err != nil {
		t.Fatal(err)
	}

	s2, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.LastProfile != "" {
		t.Errorf("last profile should be the default chain, got %q", s2.LastProfile)
	}
	for profile, want := range map[string]string{"prod": "eu-west-1", "dev": "ap-southeast-3", "": "us-east-1", "never-used": ""} {
		if got := s2.RegionFor(profile); got != want {
			t.Errorf("RegionFor(%q) = %q, want %q", profile, got, want)
		}
	}
}

func TestInMemoryStoreNeverWrites(t *testing.T) {
	s, err := LoadFrom("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TogglePin("i-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberSwitch("prod", "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	if !s.IsPinned("i-1") || s.RegionFor("prod") != "eu-west-1" {
		t.Error("in-memory store should still track state for the session")
	}
}
