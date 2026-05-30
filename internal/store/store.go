// Package store persists local lazyssm state (pinned instances) as YAML under
// the OS-correct user config directory.
package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const appDir = "lazyssm"
const fileName = "config.yaml"

// Store holds persisted state. Pinned is the set of pinned instance IDs.
type Store struct {
	Pinned []string `yaml:"pinned"`

	path   string          // resolved file path; empty when in-memory only
	pinSet map[string]bool // derived index
}

// DefaultPath returns the config file path under os.UserConfigDir().
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDir, fileName), nil
}

// Load reads the store from the default path. A missing file yields an empty,
// usable store (not an error).
func Load() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the store from an explicit path.
func LoadFrom(path string) (*Store, error) {
	s := &Store{path: path, pinSet: map[string]bool{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, s); err != nil {
		return nil, err
	}
	s.path = path
	s.reindex()
	return s, nil
}

func (s *Store) reindex() {
	s.pinSet = make(map[string]bool, len(s.Pinned))
	for _, id := range s.Pinned {
		s.pinSet[id] = true
	}
}

// IsPinned reports whether an instance ID is pinned.
func (s *Store) IsPinned(id string) bool {
	return s.pinSet[id]
}

// TogglePin flips the pinned state of an instance ID and persists the change.
// It returns the new pinned state.
func (s *Store) TogglePin(id string) (bool, error) {
	if s.pinSet == nil {
		s.pinSet = map[string]bool{}
	}
	now := !s.pinSet[id]
	s.pinSet[id] = now
	s.rebuildList()
	return now, s.Save()
}

func (s *Store) rebuildList() {
	s.Pinned = s.Pinned[:0]
	for id, on := range s.pinSet {
		if on {
			s.Pinned = append(s.Pinned, id)
		}
	}
	sort.Strings(s.Pinned)
}

// Save writes the store to its path, creating parent directories as needed.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}
