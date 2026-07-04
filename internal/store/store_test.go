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

func TestAutoLoginRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	s.AutoLogin = true
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.AutoLogin {
		t.Fatal("auto_login did not persist across reload")
	}

	// A pin-toggle save must not clobber the setting.
	if _, err := s2.TogglePin("i-1"); err != nil {
		t.Fatal(err)
	}
	s3, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s3.AutoLogin {
		t.Error("auto_login lost after TogglePin save")
	}
}

func TestAutoLoginOmittedWhenUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Legacy config without the key loads as disabled.
	if err := os.WriteFile(path, []byte("pinned:\n  - i-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.AutoLogin {
		t.Error("legacy config should default auto_login to false")
	}

	// A save from that store must not inject the key.
	if _, err := s.TogglePin("i-2"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "auto_login") {
		t.Errorf("unset auto_login should be omitted from saves, got:\n%s", data)
	}
}
