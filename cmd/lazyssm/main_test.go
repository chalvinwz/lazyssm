package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/store"
)

func savedState(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.LoadFrom(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RememberSwitch("dev", "ap-southeast-3"); err != nil {
		t.Fatal(err)
	}
	if err := st.RememberSwitch("prod", "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	return st
}

func clearAWSEnv(t *testing.T) {
	for _, k := range []string{"AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION"} {
		t.Setenv(k, "")
	}
}

func TestStartProfilePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		flagProfile, flagRegion string
		env                     map[string]string
		wantProfile, wantRegion string
		wantFromState           bool
	}{
		{name: "saved state when nothing else is set", wantProfile: "prod", wantRegion: "eu-west-1", wantFromState: true},
		{name: "flag profile gets its remembered region", flagProfile: "dev", wantProfile: "dev", wantRegion: "ap-southeast-3"},
		{name: "flags win", flagProfile: "dev", flagRegion: "us-east-1", wantProfile: "dev", wantRegion: "us-east-1"},
		{name: "AWS_PROFILE beats the saved profile", env: map[string]string{"AWS_PROFILE": "dev"}, wantProfile: "dev", wantRegion: "ap-southeast-3"},
		{name: "AWS_REGION beats the remembered region", env: map[string]string{"AWS_REGION": "us-west-2"}, wantProfile: "prod", wantFromState: true},
		{name: "AWS_DEFAULT_REGION too", env: map[string]string{"AWS_DEFAULT_REGION": "us-west-2"}, wantProfile: "prod", wantFromState: true},
		{name: "a profile never switched to has no region", flagProfile: "new", wantProfile: "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAWSEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			profile, region, fromState := startProfile(tc.flagProfile, tc.flagRegion, savedState(t))
			if profile != tc.wantProfile || region != tc.wantRegion || fromState != tc.wantFromState {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", profile, region, fromState, tc.wantProfile, tc.wantRegion, tc.wantFromState)
			}
		})
	}
}

// TestRemovedProfileFailsToLoad backs runTUI's fallback: Load fails for a saved
// profile gone from the AWS config instead of silently using another account.
func TestRemovedProfileFailsToLoad(t *testing.T) {
	clearAWSEnv(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	if err := os.WriteFile(config, []byte("[default]\nregion = us-east-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	if _, err := awscfg.Load(context.Background(), "removed", ""); err == nil {
		t.Fatal("loading a profile missing from the config should fail")
	}
	if _, err := awscfg.Load(context.Background(), "", ""); err != nil {
		t.Fatalf("the default profile should still load: %v", err)
	}
}

func TestProfileHelp(t *testing.T) {
	clearAWSEnv(t)
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	write := func(body string) {
		if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	help := func(profile string) string {
		_, err := awscfg.Load(context.Background(), profile, "")
		lines, ok := profileHelp(err)
		if !ok {
			t.Fatalf("%q: a missing profile should get help, err = %v", profile, err)
		}
		return strings.Join(lines, "\n")
	}

	write("[default]\nregion = us-east-1\n[profile acme-dev-network-admin]\nregion = ap-southeast-3\n")
	got := help("acme-dve-network-admin")
	if !strings.Contains(got, `profile "acme-dve-network-admin" isn't in `+config) || !strings.Contains(got, "did you mean acme-dev-network-admin?") {
		t.Errorf("a typo should name the file and suggest the profile:\n%s", got)
	}
	if got = help("haha"); strings.Contains(got, "did you mean") || !strings.Contains(got, "aws configure list-profiles") {
		t.Errorf("with nothing close, point at the full list:\n%s", got)
	}
	write("")
	if got = help("haha"); !strings.Contains(got, "no profiles are set up yet") {
		t.Errorf("an empty config should say so:\n%s", got)
	}
	if _, ok := profileHelp(errors.New("other")); ok {
		t.Error("other load errors get no profile help")
	}
}

func TestRegionHelp(t *testing.T) {
	for _, region := range []string{"", "us-east-1", "eu-north-9"} {
		if help := regionHelp(region, "--region"); help != nil {
			t.Errorf("%q should start: %v", region, help)
		}
	}
	got := strings.Join(regionHelp("sydney", "--region"), "\n")
	if !strings.Contains(got, `region "sydney" (from --region)`) || !strings.Contains(got, "did you mean ap-southeast-2 (Asia Pacific (Sydney))?") {
		t.Errorf("a city name should suggest its region:\n%s", got)
	}
	if got = strings.Join(regionHelp("haha", "AWS_REGION"), "\n"); !strings.Contains(got, "unset AWS_REGION") || strings.Contains(got, "did you mean") {
		t.Errorf("with nothing close, say how to get past it:\n%s", got)
	}
}

func TestRegionSource(t *testing.T) {
	clearAWSEnv(t)
	if got := regionSource("us-east-1", "us-east-1"); got != "--region" {
		t.Errorf("flag: got %q", got)
	}
	if got := regionSource("", "ap-southeast-3"); got != "state.yaml" {
		t.Errorf("remembered region: got %q", got)
	}
	t.Setenv("AWS_DEFAULT_REGION", "x")
	if got := regionSource("", ""); got != "AWS_DEFAULT_REGION" {
		t.Errorf("env: got %q", got)
	}
}
