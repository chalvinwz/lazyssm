// Package preflight verifies external binaries and AWS credentials/SSO.
//
// Checks are intentionally version-free: lazyssm verifies that a binary is
// present and runnable, never which version it is, so install guidance never
// goes stale. Guidance anchors on the stable AWS documentation URLs plus a
// high-stability convenience command; lazyssm never auto-installs.
package preflight

import (
	"context"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	awsCLIDocURL = "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
	pluginDocURL = "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"
)

// Check is the result of a single preflight check.
type Check struct {
	Name string
	OK   bool
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

// binaryCheck verifies a binary is on PATH and runnable via a version probe.
func binaryCheck(name, docURL, brew string) Check {
	c := Check{Name: name}
	path, err := exec.LookPath(name)
	if err != nil {
		c.Detail = "not found on PATH"
		c.Fix = installFix(name, docURL, brew)
		return c
	}
	// Probe runnability; presence alone is not enough.
	if err := exec.Command(name, "--version").Run(); err != nil {
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

// BinariesOK reports whether both required binaries pass.
func BinariesOK() bool {
	for _, c := range CheckBinaries() {
		if !c.OK {
			return false
		}
	}
	return true
}

// CheckIdentity validates that credentials resolve to a usable identity via
// sts:GetCallerIdentity, distinguishing an expired SSO session from a generic
// credentials failure.
func CheckIdentity(ctx context.Context, p Params) Check {
	c := Check{Name: "AWS identity"}
	if p.STS == nil {
		c.Detail = "no STS client"
		c.Fix = "internal error: STS client not constructed"
		return c
	}
	out, err := p.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		if isSSOExpired(err) {
			c.Detail = "SSO session expired or invalid"
			c.Fix = "aws sso login" + profileSuffix(p.Profile)
			return c
		}
		c.Detail = "credentials did not resolve: " + err.Error()
		c.Fix = "configure credentials (aws configure" + profileSuffix(p.Profile) + ") or set AWS_PROFILE"
		return c
	}
	c.OK = true
	acct := ""
	if out.Account != nil {
		acct = *out.Account
	}
	c.Detail = "account " + acct + " (profile " + orDefault(p.Profile) + ")"
	return c
}

// CheckRegion verifies a region was resolved.
func CheckRegion(region string) Check {
	if region == "" {
		return Check{
			Name:   "AWS region",
			Detail: "no region resolved",
			Fix:    "pass --region, set AWS_REGION, or set region in your profile",
		}
	}
	return Check{Name: "AWS region", OK: true, Detail: region}
}

// isSSOExpired heuristically detects an expired/invalid SSO token from an error.
func isSSOExpired(err error) bool {
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "sso") && !strings.Contains(s, "token") {
		return false
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

// Run executes the full check set: binaries, identity, region.
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
