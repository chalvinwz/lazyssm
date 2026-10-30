// Package preflight checks the external binaries, region and AWS credentials
// lazyssm needs. Binary checks test only that a binary runs, never its version,
// so install guidance (stable AWS docs URLs and a brew command) does not go
// stale. lazyssm never installs anything itself.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
)

const (
	awsCLIDocURL = "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
	pluginDocURL = "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"
)

// Check is the result of a single preflight check.
type Check struct {
	Name string
	OK   bool
	// Binary marks a check that probes an external binary, so a cached result
	// set can be reused without spawning the binaries again.
	Binary bool
	// SSOExpired marks an identity failure caused by an expired or invalid SSO
	// session, so callers can trigger auto-login without matching Detail text.
	SSOExpired bool
	// Account is the AWS account ID a passing identity check resolved.
	Account string
	// Detail describes the resolved state when OK, or the problem when not.
	Detail string
	// Fix is actionable guidance shown when OK is false.
	Fix string
}

// STSAPI is the subset of the STS client preflight needs.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Params carries the resolved context for credential checks.
type Params struct {
	Profile string
	Region  string
	STS     STSAPI
}

// Seams over os/exec, swapped by tests so binary checks need no real binaries.
var (
	lookPath   = exec.LookPath
	runVersion = func(name string) error { return exec.Command(name, "--version").Run() }
)

// binaryCheck verifies a binary is on PATH and runnable via a version probe.
func binaryCheck(name, docURL, brew string) Check {
	c := Check{Name: name, Binary: true}
	path, err := lookPath(name)
	if err != nil {
		c.Detail = "not found on PATH"
		c.Fix = installFix(name, docURL, brew)
		return c
	}
	// Probe runnability; presence alone is not enough.
	if err := runVersion(name); err != nil {
		c.Detail = "found at " + path + " but failed to run"
		c.Fix = installFix(name, docURL, brew)
		return c
	}
	c.OK = true
	c.Detail = path
	return c
}

func installFix(name, docURL, brew string) string {
	var b strings.Builder
	b.WriteString("install ")
	b.WriteString(name)
	b.WriteString("\n  docs: ")
	b.WriteString(docURL)
	if runtime.GOOS == "darwin" {
		b.WriteString("\n  macOS: ")
		b.WriteString(brew)
	}
	return b.String()
}

// CheckAWSCLI verifies the aws CLI is installed and runnable.
func CheckAWSCLI() Check {
	c := binaryCheck("aws", awsCLIDocURL, "brew install awscli")
	c.Name = "aws CLI"
	return c
}

// CheckPlugin verifies the session-manager-plugin is installed and runnable.
func CheckPlugin() Check {
	c := binaryCheck("session-manager-plugin", pluginDocURL, "brew install --cask session-manager-plugin")
	return c
}

// CheckBinaries returns the binary checks (aws CLI, session-manager-plugin).
func CheckBinaries() []Check {
	return []Check{CheckAWSCLI(), CheckPlugin()}
}

// BinariesOKFrom reports whether every binary check in an already-run result
// set passed, letting callers reuse a cached preflight instead of re-probing.
func BinariesOKFrom(checks []Check) bool {
	for _, c := range checks {
		if c.Binary && !c.OK {
			return false
		}
	}
	return true
}

// SSOExpiredFrom reports whether any check in an already-run result set failed
// because of an expired SSO session.
func SSOExpiredFrom(checks []Check) bool {
	for _, c := range checks {
		if c.SSOExpired {
			return true
		}
	}
	return false
}

// CheckIdentity verifies through sts:GetCallerIdentity that the credentials
// resolve, telling an expired SSO session apart from other failures.
func CheckIdentity(ctx context.Context, p Params) Check {
	c := Check{Name: "AWS identity"}
	if p.STS == nil {
		c.Detail = "no STS client"
		c.Fix = "internal error: STS client not constructed"
		return c
	}
	out, err := p.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		if IsSSOExpired(err) {
			c.SSOExpired = true
			c.Detail = "SSO session expired or invalid"
			c.Fix = "aws sso login" + profileSuffix(p.Profile)
			return c
		}
		c.Detail = "credentials did not resolve: " + err.Error()
		c.Fix = "configure credentials (aws configure" + profileSuffix(p.Profile) + ") or set AWS_PROFILE"
		return c
	}
	c.OK = true
	if out.Account != nil {
		c.Account = *out.Account
	}
	c.Detail = "account " + c.Account + " (profile " + orDefault(p.Profile) + ")"
	return c
}

// AccountFrom returns the account ID from an already-run result set, or "" when
// the identity check did not resolve one.
func AccountFrom(checks []Check) string {
	for _, c := range checks {
		if c.Account != "" {
			return c.Account
		}
	}
	return ""
}

// CheckRegion verifies a region was resolved and looks like a region code. A
// well-formed code lazyssm does not list passes, since AWS adds regions.
func CheckRegion(region string) Check {
	if region == "" {
		return Check{
			Name:   "AWS region",
			Detail: "no region resolved",
			Fix:    "pass --region, set AWS_REGION, or set region in your profile",
		}
	}
	if !awscfg.LooksLikeRegion(region) {
		c := Check{Name: "AWS region", Detail: fmt.Sprintf("%q isn't an AWS region code", region), Fix: "region codes look like ap-southeast-3"}
		if near := RegionHint(region); near != "" {
			c.Fix = "did you mean " + near + "?"
		}
		return c
	}
	return Check{Name: "AWS region", OK: true, Detail: region}
}

// RegionHint names the regions a value that is not a region code most likely
// meant, such as "ap-southeast-3 (Asia Pacific (Jakarta))", or "" for none.
func RegionHint(region string) string {
	var names []string
	for _, r := range awscfg.SuggestRegions(region) {
		names = append(names, r.Code+" ("+r.Name+")")
	}
	return strings.Join(names, ", ")
}

// IsSSOExpired reports whether err comes from an expired or invalid SSO token.
// It prefers the AWS error code and falls back to matching the text of
// credential-chain errors that are not smithy APIErrors. It is exported so the
// UI can classify mid-session fetch errors too.
func IsSSOExpired(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ExpiredToken", "ExpiredTokenException":
			return true
		case "UnrecognizedClientException", "InvalidClientTokenId", "AuthFailure":
			// "The security token included in the request is invalid": bad
			// keys or a region the account has not opted into, not an expired
			// SSO session, so signing in again would not help.
			return false
		}
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "sso") && !strings.Contains(s, "token") {
		return false
	}
	// A missing or unreadable cached SSO token, as after `aws sso logout`
	// removes the cache file, needs the same remedy as an expired one.
	if strings.Contains(s, "sso") &&
		(strings.Contains(s, "no such file") || strings.Contains(s, "failed to read")) {
		return true
	}
	return strings.Contains(s, "expired") ||
		strings.Contains(s, "invalid") ||
		strings.Contains(s, "re-authenticate") ||
		strings.Contains(s, "login")
}

func profileSuffix(profile string) string {
	if profile == "" {
		return ""
	}
	return " --profile " + profile
}

func orDefault(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}

// Run executes the full check set: binaries, region, identity.
func Run(ctx context.Context, p Params) []Check {
	checks := CheckBinaries()
	checks = append(checks, CheckRegion(p.Region))
	checks = append(checks, CheckIdentity(ctx, p))
	return checks
}

// AllOK reports whether every check in the slice passed.
func AllOK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}
