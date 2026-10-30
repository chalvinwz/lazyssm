package awscfg

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
)

// Profile is a named profile from the shared AWS config or credentials file.
type Profile struct {
	Name      string
	Region    string // the profile's region setting, if any
	AccountID string // sso_account_id, if any
}

// Profiles lists the profiles in the files SharedFiles names, "default" first
// and the rest by name. Missing files are not an error.
func Profiles() ([]Profile, error) {
	return profilesFrom(SharedFiles())
}

// SharedFiles returns the shared config and credentials file paths the SDK
// reads, honoring AWS_CONFIG_FILE and AWS_SHARED_CREDENTIALS_FILE.
func SharedFiles() (configPath, credentialsPath string) {
	return envOr("AWS_CONFIG_FILE", config.DefaultSharedConfigFilename()),
		envOr("AWS_SHARED_CREDENTIALS_FILE", config.DefaultSharedCredentialsFilename())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func profilesFrom(configPath, credentialsPath string) ([]Profile, error) {
	byName := map[string]*Profile{}
	if err := readProfiles(configPath, true, byName); err != nil {
		return nil, err
	}
	if err := readProfiles(credentialsPath, false, byName); err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(byName))
	for _, p := range byName {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == "default") != (out[j].Name == "default") {
			return out[i].Name == "default"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// readProfiles adds the profiles in one INI file to byName. In the config file
// only [default] and [profile name] sections are profiles; in the credentials
// file every section is. A region already read from the config file is kept.
func readProfiles(path string, isConfig bool, byName map[string]*Profile) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var cur *Profile
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			cur = nil
			end := strings.IndexByte(line, ']')
			if end < 0 {
				continue
			}
			name := strings.TrimSpace(line[1:end])
			if isConfig && name != "default" {
				rest, ok := strings.CutPrefix(name, "profile ")
				if !ok {
					continue
				}
				name = strings.TrimSpace(rest)
			}
			if name == "" {
				continue
			}
			if byName[name] == nil {
				byName[name] = &Profile{Name: name}
			}
			cur = byName[name]
			continue
		}
		// Indented lines are nested settings, such as those under "s3 =".
		if cur == nil || raw[0] == ' ' || raw[0] == '\t' {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "region":
			if cur.Region == "" {
				cur.Region = value
			}
		case "sso_account_id":
			cur.AccountID = value
		}
	}
	return sc.Err()
}

// MissingProfile reports whether err says a profile is missing from the shared
// config and credentials files, and returns the profile's name.
func MissingProfile(err error) (profile string, ok bool) {
	var missing config.SharedConfigProfileNotExistError
	if !errors.As(err, &missing) {
		return "", false
	}
	return missing.Profile, true
}

// Suggest picks up to three profile names close to name: a typo away from it,
// or containing it ("prod" finds "acme-prod-admin"). Closest come first.
func Suggest(name string, profiles []Profile) []string {
	type candidate struct {
		name     string
		distance int
	}
	want := strings.ToLower(name)
	var found []candidate
	for _, p := range profiles {
		have := strings.ToLower(p.Name)
		d := editDistance(want, have)
		if d <= max(1, len(want)/3) || (len(want) >= 3 && strings.Contains(have, want)) {
			found = append(found, candidate{p.Name, d})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].distance < found[j].distance })
	out := make([]string, 0, 3)
	for _, c := range found[:min(3, len(found))] {
		out = append(out, c.name)
	}
	return out
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
