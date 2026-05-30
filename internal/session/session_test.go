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
