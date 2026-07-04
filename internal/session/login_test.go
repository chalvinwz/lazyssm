package session

import (
	"strings"
	"testing"
)

func TestBuildLoginArgs(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		region  string
		want    string
	}{
		{"both", "dev", "eu-west-1", "sso login --profile dev --region eu-west-1"},
		{"neither", "", "", "sso login"},
		{"profile only", "dev", "", "sso login --profile dev"},
		{"region only", "", "eu-west-1", "sso login --region eu-west-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(buildLoginArgs(tc.profile, tc.region), " ")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
