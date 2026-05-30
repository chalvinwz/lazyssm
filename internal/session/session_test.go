package session

import (
	"strings"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	got := buildArgs("i-123", "dev", "eu-west-1")
	want := "ssm start-session --target i-123 --profile dev --region eu-west-1"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestBuildArgsNoProfileRegion(t *testing.T) {
	got := buildArgs("i-123", "", "")
	want := "ssm start-session --target i-123"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestBuildArgsProfileOnly(t *testing.T) {
	got := buildArgs("i-123", "dev", "")
	want := "ssm start-session --target i-123 --profile dev"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestBuildArgsRegionOnly(t *testing.T) {
	got := buildArgs("i-123", "", "eu-west-1")
	want := "ssm start-session --target i-123 --region eu-west-1"
	if strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

// TestBuildArgsKeepsInstanceIDAsSingleArg documents the no-shell safety:
// exec.Command runs aws directly (no /bin/sh), so even shell metacharacters in
// the target ID stay one literal argv element — never split or interpreted.
func TestBuildArgsKeepsInstanceIDAsSingleArg(t *testing.T) {
	id := `i-1"; rm -rf / #`
	got := buildArgs(id, "", "")
	if len(got) != 4 {
		t.Fatalf("want 4 args, got %d: %#v", len(got), got)
	}
	if got[3] != id {
		t.Errorf("instance id must be one literal arg, got %q", got[3])
	}
}
