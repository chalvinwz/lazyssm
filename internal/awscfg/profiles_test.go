package awscfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfilesFromConfigAndCredentials(t *testing.T) {
	dir := t.TempDir()
	configPath := writeFile(t, dir, "config", `# shared config
[default]
region = us-east-1

[profile prod]
sso_session = corp
sso_account_id = 111122223333
region = eu-west-1

[sso-session corp]
sso_region = us-east-1

[services local]
s3 =
  endpoint_url = http://localhost:9000

[profile dev]
s3 =
  region = should-be-ignored
region=ap-southeast-3
; trailing comment
`)
	credentialsPath := writeFile(t, dir, "credentials", `[default]
aws_access_key_id = AKIAEXAMPLE
region = us-west-2

[legacy]
region = ap-south-1
`)

	got, err := profilesFrom(configPath, credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{
		{Name: "default", Region: "us-east-1"}, // config file region wins
		{Name: "dev", Region: "ap-southeast-3"},
		{Name: "legacy", Region: "ap-south-1"}, // credentials file only
		{Name: "prod", Region: "eu-west-1", AccountID: "111122223333"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("profiles:\n got %+v\nwant %+v", got, want)
	}
}

func TestProfilesMissingFiles(t *testing.T) {
	dir := t.TempDir()
	got, err := profilesFrom(filepath.Join(dir, "nope"), filepath.Join(dir, "also-nope"))
	if err != nil {
		t.Fatalf("missing files should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no profiles, got %+v", got)
	}
}

func TestProfilesHonourEnvPaths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", writeFile(t, dir, "my-config", "[profile from-env]\n"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	got, err := Profiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "from-env" {
		t.Errorf("want the profile from AWS_CONFIG_FILE, got %+v", got)
	}
}

func TestLooksLikeRegion(t *testing.T) {
	for s, want := range map[string]bool{
		"eu-west-1":      true,
		"us-gov-west-1":  true,
		"ap-southeast-7": true,
		"eu-west":        false,
		"EU-WEST-1":      false,
		"prod":           false,
		"":               false,
	} {
		if got := LooksLikeRegion(s); got != want {
			t.Errorf("LooksLikeRegion(%q) = %v, want %v", s, got, want)
		}
	}
	for _, r := range Regions {
		if !LooksLikeRegion(r.Code) {
			t.Errorf("built-in region %q fails the pattern", r.Code)
		}
	}
}

func TestSuggest(t *testing.T) {
	profiles := []Profile{{Name: "default"}, {Name: "acme-dev-network-admin"}, {Name: "acme-prod-admin"}, {Name: "sandbox"}}
	for name, want := range map[string][]string{
		"acme-dve-network-admin": {"acme-dev-network-admin"}, // swapped letters
		"Sandbx":                 {"sandbox"},
		"prod":                   {"acme-prod-admin"},
		"haha":                   {},
	} {
		got := Suggest(name, profiles)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("Suggest(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMissingProfileFromLoad(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte("[default]\nregion = us-east-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION"} {
		t.Setenv(k, "")
	}
	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	_, err := Load(context.Background(), "haha", "")
	if name, ok := MissingProfile(err); !ok || name != "haha" {
		t.Errorf("MissingProfile(%v) = %q, %v", err, name, ok)
	}
	if got, _ := SharedFiles(); got != config {
		t.Errorf("SharedFiles should honor AWS_CONFIG_FILE, got %q", got)
	}
	if _, ok := MissingProfile(errors.New("other")); ok {
		t.Error("other errors are not a missing profile")
	}
}

func TestSuggestRegions(t *testing.T) {
	for in, want := range map[string]string{
		"ap-southeast3":  "ap-southeast-3",
		"AP-SOUTHEAST-3": "ap-southeast-3",
		"us-east":        "us-east-1,us-east-2",
		"sydney":         "ap-southeast-2",
		"sao paulo":      "sa-east-1",
		"haha":           "",
	} {
		var codes []string
		for _, r := range SuggestRegions(in) {
			codes = append(codes, r.Code)
		}
		if got := strings.Join(codes, ","); got != want {
			t.Errorf("SuggestRegions(%q) = %q, want %q", in, got, want)
		}
	}
}
