package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

func TestBinaryCheck(t *testing.T) {
	origLook, origRun := lookPath, runVersion
	defer func() { lookPath, runVersion = origLook, origRun }()

	cases := []struct {
		name       string
		look       func(string) (string, error)
		run        func(string) error
		wantOK     bool
		wantDetail string // exact for OK/not-found, prefix for run-failure
		wantFix    bool
	}{
		{
			name:       "not found on PATH",
			look:       func(string) (string, error) { return "", errors.New("not found") },
			run:        func(string) error { return nil },
			wantDetail: "not found on PATH",
			wantFix:    true,
		},
		{
			name:       "found but version probe fails",
			look:       func(string) (string, error) { return "/usr/bin/aws", nil },
			run:        func(string) error { return errors.New("exit 1") },
			wantDetail: "found at /usr/bin/aws",
			wantFix:    true,
		},
		{
			name:       "present and runnable",
			look:       func(string) (string, error) { return "/usr/bin/aws", nil },
			run:        func(string) error { return nil },
			wantOK:     true,
			wantDetail: "/usr/bin/aws",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookPath, runVersion = tc.look, tc.run
			c := binaryCheck("aws", "https://docs", "brew install awscli")
			if c.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v", c.OK, tc.wantOK)
			}
			if tc.wantOK || tc.wantDetail == "not found on PATH" {
				if c.Detail != tc.wantDetail {
					t.Errorf("Detail = %q, want %q", c.Detail, tc.wantDetail)
				}
			} else if !strings.HasPrefix(c.Detail, tc.wantDetail) {
				t.Errorf("Detail = %q, want prefix %q", c.Detail, tc.wantDetail)
			}
			if (c.Fix != "") != tc.wantFix {
				t.Errorf("Fix present = %v, want %v (got %q)", c.Fix != "", tc.wantFix, c.Fix)
			}
		})
	}
}

func TestIsSSOExpired(t *testing.T) {
	cases := map[string]bool{
		"the SSO session associated with this profile has expired": true,
		"sso token is invalid, please re-authenticate":             true,
		"failed to refresh cached credentials, token expired":      true,
		// after `aws sso logout` the cache file is deleted, not expired
		"operation error STS: GetCallerIdentity, get identity: get credentials: failed to refresh cached credentials, failed to read cached SSO token file, open /home/u/.aws/sso/cache/abc.json: no such file or directory": true,
		"AccessDenied: not authorized": false,
		"no EC2 IMDS role found":       false,
		// missing non-SSO file must not classify as SSO-expired
		"failed to load config, open /home/u/.aws/config: no such file or directory": false,
	}
	for msg, want := range cases {
		if got := IsSSOExpired(errors.New(msg)); got != want {
			t.Errorf("IsSSOExpired(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestIsSSOExpiredTypedCode(t *testing.T) {
	// A typed AWS error is detected by code even without SSO wording in the text.
	err := &smithy.GenericAPIError{Code: "ExpiredToken", Message: "the security token included in the request is expired"}
	if !IsSSOExpired(err) {
		t.Error("ExpiredToken APIError should be detected")
	}
	other := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
	if IsSSOExpired(other) {
		t.Error("AccessDeniedException should not be treated as SSO-expired")
	}
}

func TestBinariesOKFrom(t *testing.T) {
	ok := []Check{{Name: "aws CLI", Binary: true, OK: true}, {Name: "plugin", Binary: true, OK: true}, {Name: "region", OK: false}}
	if !BinariesOKFrom(ok) {
		t.Error("binary checks all pass -> true (non-binary failures ignored)")
	}
	bad := []Check{{Name: "aws CLI", Binary: true, OK: false}, {Name: "plugin", Binary: true, OK: true}}
	if BinariesOKFrom(bad) {
		t.Error("a failed binary check -> false")
	}
}

func TestCheckRegion(t *testing.T) {
	if c := CheckRegion(""); c.OK {
		t.Error("empty region should fail")
	}
	if c := CheckRegion("us-east-1"); !c.OK || c.Detail != "us-east-1" {
		t.Errorf("region check = %+v", c)
	}
}

type fakeSTS struct {
	err error
}

func (f fakeSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
}

func TestCheckIdentityExpiredSSO(t *testing.T) {
	c := CheckIdentity(context.Background(), Params{
		Profile: "dev",
		STS:     fakeSTS{err: errors.New("the SSO token has expired")},
	})
	if c.OK {
		t.Fatal("expired SSO should fail identity check")
	}
	if !c.SSOExpired {
		t.Error("SSOExpired should be set on an expired-SSO failure")
	}
	if c.Fix != "aws sso login --profile dev" {
		t.Errorf("want sso login fix, got %q", c.Fix)
	}
}

func TestCheckIdentityGenericErrorNotSSOExpired(t *testing.T) {
	c := CheckIdentity(context.Background(), Params{
		STS: fakeSTS{err: errors.New("AccessDenied: not authorized")},
	})
	if c.OK {
		t.Fatal("generic credential error should fail identity check")
	}
	if c.SSOExpired {
		t.Error("SSOExpired should not be set on a non-SSO failure")
	}
}

func TestSSOExpiredFrom(t *testing.T) {
	cases := []struct {
		name   string
		checks []Check
		want   bool
	}{
		{"empty", nil, false},
		{"all ok", []Check{{OK: true}, {OK: true}}, false},
		{"failed but not sso", []Check{{OK: false}}, false},
		{"sso expired", []Check{{OK: true}, {OK: false, SSOExpired: true}}, true},
	}
	for _, tc := range cases {
		if got := SSOExpiredFrom(tc.checks); got != tc.want {
			t.Errorf("%s: SSOExpiredFrom = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCheckIdentityOK(t *testing.T) {
	c := CheckIdentity(context.Background(), Params{STS: fakeSTS{}})
	if !c.OK {
		t.Fatalf("valid identity should pass, got %+v", c)
	}
}
