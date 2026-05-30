package preflight

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

func TestIsSSOExpired(t *testing.T) {
	cases := map[string]bool{
		"the SSO session associated with this profile has expired": true,
		"sso token is invalid, please re-authenticate":             true,
		"failed to refresh cached credentials, token expired":      true,
		"AccessDenied: not authorized":                             false,
		"no EC2 IMDS role found":                                   false,
	}
	for msg, want := range cases {
		if got := isSSOExpired(errors.New(msg)); got != want {
			t.Errorf("isSSOExpired(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestIsSSOExpiredTypedCode(t *testing.T) {
	// A typed AWS error is detected by code even without SSO wording in the text.
	err := &smithy.GenericAPIError{Code: "ExpiredToken", Message: "the security token included in the request is expired"}
	if !isSSOExpired(err) {
		t.Error("ExpiredToken APIError should be detected")
	}
	other := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
	if isSSOExpired(other) {
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
	if c.Fix != "aws sso login --profile dev" {
		t.Errorf("want sso login fix, got %q", c.Fix)
	}
}

func TestCheckIdentityOK(t *testing.T) {
	c := CheckIdentity(context.Background(), Params{STS: fakeSTS{}})
	if !c.OK {
		t.Fatalf("valid identity should pass, got %+v", c)
	}
}
