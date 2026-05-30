package store

import (
	"path/filepath"
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
