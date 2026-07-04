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
	// AutoLogin persistently enables running `aws sso login` when the SSO
	// session expires (equivalent to the --auto-login flag). omitempty keeps
	// pin-toggle saves from injecting the key into configs that never set it.
	AutoLogin bool `yaml:"auto_login,omitempty"`

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
// The write is atomic: data goes to a temp file in the same directory and is
// renamed into place, so a crash or full disk mid-write can't truncate an
// existing config and lose pins.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path) // atomic on POSIX
}
