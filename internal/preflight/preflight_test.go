package preflight

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
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
