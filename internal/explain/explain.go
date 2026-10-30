// Package explain turns failed AWS calls into a short, plain explanation for
// the TUI, keeping the full error text for anyone who wants to copy it.
package explain

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/preflight"
)

// Explanation is what lazyssm shows for a failed AWS call.
type Explanation struct {
	Headline string      // one line for the list pane, e.g. "No SSM access in ap-southeast-2"
	Status   string      // one line for the status bar
	Lines    []string    // why it failed and what to try, in plain words
	Code     string      // the AWS error code, if AWS answered
	Facts    [][2]string // key/value lines for the error panel
	Raw      string      // the full error text
	SignIn   bool        // an expired SSO session: aws sso login fixes it
	Service  string      // the AWS service whose call failed, e.g. "SSM"
}

// denial is what an IAM access-denied message says was refused, and by which
// kind of policy. AWS documents the format as "User: … is not authorized to
// perform: ACTION [on resource: …] with an explicit deny in a TYPE[: ARN]" or
// "… because no TYPE allows the ACTION action".
type denial struct {
	action     string // ssm:DescribeInstanceInformation
	policyType string // "service control policy", "permissions boundary", …
	policyARN  string
	explicit   bool
}

var actionRE = regexp.MustCompile(`not authorized to perform: ([a-z0-9-]+:[A-Za-z0-9*]+)`)

// Error explains err from a call lazyssm made in region.
func Error(err error, region string) Explanation {
	x := Explanation{Raw: err.Error()}
	service, operation := "AWS", ""
	if op := innermostOperation(err); op != nil {
		// The innermost call is the one that failed: STS AssumeRole or SSO
		// GetRoleCredentials when credentials cannot be obtained, else the SSM
		// or EC2 call itself.
		service, operation = op.ServiceID, op.OperationName
	}
	x.Service = service
	message := ""
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		x.Code, message = apiErr.ErrorCode(), apiErr.ErrorMessage()
	}
	in := ""
	if region != "" {
		in = " in " + region
	}
	denied := isDenied(x.Code, message)
	if operation != "" && !denied { // a denial names the action instead
		x.Facts = append(x.Facts, [2]string{"call", service + " " + operation})
	}

	switch {
	case x.Code == "ForbiddenException" && operation == "GetRoleCredentials":
		x.Headline = "No access to the profile's SSO role"
		x.Status = "no access to the profile's SSO role"
		x.Lines = []string{
			"IAM Identity Center doesn't give you the role in this profile (sso_role_name in sso_account_id).",
			"Ask your AWS admin to assign it, or pick another profile with P.",
		}
	case denied:
		d := parseDenial(message)
		if d.action == "" && operation != "" {
			d.action = strings.ToLower(service) + ":" + operation
		}
		x.Headline = "No " + service + " access" + in
		x.Status = "no " + service + " access" + in
		if service == "STS" && strings.HasPrefix(operation, "AssumeRole") {
			x.Headline = "Can't assume the profile's role"
			x.Status = "can't assume the profile's role"
		}
		x.Lines = denialLines(d)
		x.Facts = append(x.Facts, [2]string{"action", d.action})
		if d.policyType != "" {
			x.Status += " (" + d.policyType + ")"
			why := "no " + d.policyType + " allows it"
			if d.explicit {
				why = "by " + d.policyType
			}
			x.Facts = append(x.Facts, [2]string{"denied", why}, [2]string{"policy", policyID(d.policyARN)})
		}
	case isBadToken(x.Code, message):
		x.Headline = "AWS rejected the credentials" + in
		x.Status = "AWS rejected the credentials" + in
		if region != "" {
			x.Lines = append(x.Lines, "If "+region+" is an opt-in region, it may not be enabled for this account.")
		}
		x.Lines = append(x.Lines, "Otherwise the credentials are invalid: check the profile, or sign in again.")
	case preflight.IsSSOExpired(err):
		x.Headline = "AWS SSO session expired"
		x.Status = "SSO session expired"
		x.Lines = []string{"Sign in again with aws sso login."}
		x.SignIn = true
	// The credential cases come before the network ones: looking for an EC2
	// instance role off EC2 fails with a timeout or an unreachable address.
	case strings.Contains(x.Raw, "error in credential_process: "):
		_, why, _ := strings.Cut(x.Raw, "error in credential_process: ")
		x.Headline = "The profile's credential_process failed"
		x.Status = "credential_process failed"
		x.Lines = []string{"It exited with an error (" + why + "). Run it in a terminal to see why."}
	case strings.Contains(x.Raw, "no EC2 IMDS role found"):
		x.Headline = "No AWS credentials found"
		x.Status = "no AWS credentials found"
		x.Lines = []string{
			"lazyssm found no credentials for this profile.",
			"Set it up with aws configure or aws configure sso, or pick another profile with P.",
		}
	case isMissingProfile(err):
		name, _ := awscfg.MissingProfile(err)
		x.Headline = "Profile " + name + " isn't in your AWS config"
		x.Status = "profile " + name + " isn't in your AWS config"
		x.Lines = []string{"Check ~/.aws/config, or the file AWS_CONFIG_FILE names."}
	case errors.As(err, new(*aws.MissingRegionError)) || strings.Contains(x.Raw, "Missing Region"):
		// The endpoint resolver reports it as text ("Invalid Configuration:
		// Missing Region"); the typed error comes from older code paths.
		x.Headline = "No AWS region set"
		x.Status = "no AWS region set"
		x.Lines = []string{"Pick one with R, or set region in the profile, AWS_REGION or --region."}
	case strings.Contains(x.Raw, "invalid input region "):
		x.Headline = "The region isn't an AWS region code"
		x.Status = "region isn't an AWS region code"
		x.Lines = []string{"Pick one with R."}
	case x.Code == "TargetNotConnected":
		x.Headline = "The instance isn't connected to SSM"
		x.Status = "instance not connected to SSM"
		x.Lines = []string{message, "Its SSM agent is offline or can't reach SSM. Check that it's running, then press r."}
	case isThrottled(x.Code):
		x.Headline = "AWS is throttling " + service + " requests" + in
		x.Status = "throttled by " + service + in
		x.Lines = []string{"Too many requests in a short time. Wait a moment, then press r."}
	case isTimeout(err):
		x.Headline = service + in + " didn't answer in time"
		x.Status = service + " timed out" + in
		x.Lines = []string{"Check your network or VPN, then press r."}
	case isUnreachable(err):
		x.Headline = "Can't reach " + service + in
		x.Status = "can't reach " + service + in
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			x.Lines = append(x.Lines, dnsErr.Name+" doesn't exist, so the region code may be wrong.")
		}
		x.Lines = append(x.Lines, "Check the region and your network or VPN, then press r.")
	case operation == "":
		// Not an AWS call at all, e.g. loading the profile: the text is the
		// best explanation there is.
		x.Headline = "AWS request failed"
		x.Status = x.Raw
		x.Lines = []string{x.Raw}
	default:
		x.Headline = service + " " + operation + " failed" + in
		x.Status = x.Headline
		if x.Code != "" {
			x.Status += " (" + x.Code + ")"
		}
		x.Lines = []string{message}
		if message == "" {
			x.Lines = []string{x.Raw}
		}
	}

	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		x.Facts = append(x.Facts, [2]string{"request", respErr.ServiceRequestID()})
	}
	return x
}

// innermostOperation finds the deepest AWS call in err's chain. A fetch that
// fails while getting credentials wraps the STS or SSO call that failed.
func innermostOperation(err error) *smithy.OperationError {
	var found *smithy.OperationError
	for {
		var op *smithy.OperationError
		if !errors.As(err, &op) {
			return found
		}
		found, err = op, op.Err
	}
}

func isDenied(code, message string) bool {
	switch code {
	case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation":
		return true
	}
	return strings.Contains(message, "is not authorized to perform")
}

// isBadToken reports whether AWS refused the credentials outright. Besides
// invalid keys, a region the account has not opted into answers this way.
func isBadToken(code, message string) bool {
	switch code {
	case "UnrecognizedClientException", "InvalidClientTokenId", "AuthFailure":
		return true
	}
	return strings.Contains(message, "security token included in the request is invalid")
}

func isMissingProfile(err error) bool {
	_, ok := awscfg.MissingProfile(err)
	return ok
}

func isThrottled(code string) bool {
	switch code {
	case "Throttling", "ThrottlingException", "RequestLimitExceeded", "TooManyRequestsException", "RequestThrottled":
		return true
	}
	return false
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

func isUnreachable(err error) bool {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	return errors.As(err, &opErr) || errors.As(err, &dnsErr)
}

func parseDenial(message string) denial {
	var d denial
	if m := actionRE.FindStringSubmatch(message); m != nil {
		d.action = m[1]
	}
	if _, rest, ok := strings.Cut(message, "with an explicit deny in "); ok {
		d.explicit = true
		typ, arn, _ := strings.Cut(rest, ": ")
		d.policyType = cleanPolicyType(typ)
		if fields := strings.Fields(arn); len(fields) > 0 && strings.HasPrefix(fields[0], "arn:") {
			d.policyARN = fields[0]
		}
	} else if _, rest, ok := strings.Cut(message, "because no "); ok {
		typ, _, _ := strings.Cut(rest, " allows")
		d.policyType = cleanPolicyType(typ)
	}
	return d
}

func cleanPolicyType(s string) string {
	s, _, _ = strings.Cut(s, ".")
	s = strings.TrimSpace(s)
	for _, article := range []string{"a ", "an ", "the "} {
		s = strings.TrimPrefix(s, article)
	}
	return s
}

// policyID shortens a policy ARN to its last path segment: the SCP ID
// (p-…) or the IAM policy name.
func policyID(arn string) string {
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

func denialLines(d denial) []string {
	action := d.action
	if action == "" {
		action = "The request"
	}
	if d.policyType == "" {
		return []string{action + " was denied.", "Ask your AWS admin for access, or use a profile that has it."}
	}
	if !d.explicit {
		return []string{"No " + d.policyType + " allows " + action + ".", "Ask your AWS admin for access, or use a profile that has it."}
	}
	line := action + " is blocked by " + withArticle(d.policyType)
	if id := policyID(d.policyARN); id != "" {
		line += " (" + id + ")"
	}
	line += "."
	switch d.policyType {
	case "service control policy", "resource control policy":
		return []string{line, "Your AWS organization sets this policy. Organizations often allow only some regions, so another region may work."}
	}
	return []string{line, "Ask your AWS admin to change it, or use a profile that isn't blocked."}
}

func withArticle(s string) string {
	if s != "" && strings.ContainsRune("aeiou", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}
