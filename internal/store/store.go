// Package store reads lazyssm's config.yaml and saves its state.yaml under the
// user config directory. lazyssm only reads config.yaml, which the user edits;
// pins and the last profile and region go to state.yaml, so saving never
// rewrites the user's file or drops their comments.
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
const stateFileName = "state.yaml"

// Store holds the settings read from config.yaml and the state saved in
// state.yaml.
type Store struct {
	// Settings from config.yaml.
	Theme                 string   `yaml:"theme,omitempty"`
	TransparentBackground bool     `yaml:"transparent_background,omitempty"`
	Columns               []string `yaml:"columns,omitempty"`
	// AutoLogin runs `aws sso login` when the SSO session expires, as the
	// --auto-login flag does.
	AutoLogin bool `yaml:"auto_login,omitempty"`

	// State from state.yaml.
	Pinned         []string          `yaml:"-"`
	LastProfile    string            `yaml:"-"`
	ProfileRegions map[string]string `yaml:"-"` // last region browsed per profile

	path      string          // config.yaml; empty when in-memory only
	statePath string          // state.yaml next to it
	pinSet    map[string]bool // derived index
}

// state is the on-disk shape of state.yaml.
type state struct {
	Pinned         []string          `yaml:"pinned,omitempty"`
	LastProfile    string            `yaml:"last_profile,omitempty"`
	ProfileRegions map[string]string `yaml:"profile_regions,omitempty"`
}

// legacyPins reads the pins that versions before 0.5 saved in config.yaml.
type legacyPins struct {
	Pinned []string `yaml:"pinned"`
}

// DefaultPath returns the config file path under os.UserConfigDir().
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDir, fileName), nil
}

// Load reads the store from the default path. Missing files yield an empty,
// usable store rather than an error.
func Load() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads config.yaml from path and state.yaml from the same directory.
// An empty path gives an in-memory store that never touches disk.
func LoadFrom(path string) (*Store, error) {
	s := &Store{path: path, pinSet: map[string]bool{}}
	if path == "" {
		return s, nil
	}
	s.statePath = filepath.Join(filepath.Dir(path), stateFileName)

	var legacy legacyPins
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, s); err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, &legacy); err != nil {
			return nil, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}

	data, err = os.ReadFile(s.statePath)
	switch {
	case err == nil:
		var st state
		if err := yaml.Unmarshal(data, &st); err != nil {
			return nil, err
		}
		s.Pinned, s.LastProfile, s.ProfileRegions = st.Pinned, st.LastProfile, st.ProfileRegions
	case errors.Is(err, fs.ErrNotExist):
		// First run after upgrading: carry the old pins over. The first save
		// writes them to state.yaml, which from then on is the only source.
		s.Pinned = legacy.Pinned
	default:
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

// TogglePin flips whether id is pinned, saves the change and returns the new
// pinned state.
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

// RegionFor returns the region last browsed with profile, or "" if none.
func (s *Store) RegionFor(profile string) string {
	return s.ProfileRegions[profileKey(profile)]
}

// RememberSwitch records profile as the last one used and region as the one
// it was last browsed in, then saves both.
func (s *Store) RememberSwitch(profile, region string) error {
	s.LastProfile = profile
	if region != "" {
		if s.ProfileRegions == nil {
			s.ProfileRegions = map[string]string{}
		}
		s.ProfileRegions[profileKey(profile)] = region
	}
	return s.Save()
}

// profileKey names the default credential chain "default" in state.yaml.
func profileKey(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}

// Save writes state.yaml atomically through a temp file and rename, so a
// crash mid-write cannot lose pins. It never writes config.yaml.
func (s *Store) Save() error {
	if s.statePath == "" {
		return nil
	}
	dir := filepath.Dir(s.statePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(state{Pinned: s.Pinned, LastProfile: s.LastProfile, ProfileRegions: s.ProfileRegions})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.yaml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.statePath) // atomic on POSIX
}
