package explain

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// apiError builds the error chain the SDK returns for a service error reply.
func apiError(service, operation, code, message string) error {
	return &smithy.OperationError{
		ServiceID:     service,
		OperationName: operation,
		Err: &awshttp.ResponseError{
			ResponseError: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400}},
				Err:      &smithy.GenericAPIError{Code: code, Message: message},
			},
			RequestID: "384132ca-0000-4000-8000-000000000000",
		},
	}
}

func fact(x Explanation, key string) string {
	for _, kv := range x.Facts {
		if kv[0] == key {
			return kv[1]
		}
	}
	return ""
}

// TestExplainSCPRegionDeny uses the message format AWS documents for an SCP
// explicit deny.
func TestExplainSCPRegionDeny(t *testing.T) {
	err := apiError("SSM", "DescribeInstanceInformation", "AccessDeniedException",
		"User: arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_PowerUser_abc/jane@example.com is not authorized to perform: "+
			"ssm:DescribeInstanceInformation on resource: arn:aws:ssm:ap-southeast-2:123456789012:* with an explicit deny in a service control policy: "+
			"arn:aws:organizations::777788889999:policy/o-exampleorgid/service_control_policy/p-examplepolicyid123")
	x := Error(err, "ap-southeast-2")
	if x.Headline != "No SSM access in ap-southeast-2" {
		t.Errorf("headline = %q", x.Headline)
	}
	if x.Status != "no SSM access in ap-southeast-2 (service control policy)" {
		t.Errorf("status = %q", x.Status)
	}
	want := "ssm:DescribeInstanceInformation is blocked by a service control policy (p-examplepolicyid123)."
	if len(x.Lines) < 2 || x.Lines[0] != want || !strings.Contains(x.Lines[1], "another region") {
		t.Errorf("lines = %q, want first %q and a hint to try another region", x.Lines, want)
	}
	if fact(x, "policy") != "p-examplepolicyid123" || fact(x, "request") != "384132ca-0000-4000-8000-000000000000" || x.Code != "AccessDeniedException" {
		t.Errorf("facts = %v code = %q", x.Facts, x.Code)
	}
	if x.Raw != err.Error() {
		t.Error("the full error must be kept for copying")
	}
}

func TestExplainImplicitDeny(t *testing.T) {
	// EC2 leads with its own sentence; "perform this operation" is not the action.
	err := apiError("EC2", "DescribeInstances", "UnauthorizedOperation",
		"You are not authorized to perform this operation. User: arn:aws:iam::123456789012:role/HR is not authorized to perform: "+
			"ec2:DescribeInstances because no identity-based policy allows the ec2:DescribeInstances action")
	x := Error(err, "us-east-1")
	if x.Lines[0] != "No identity-based policy allows ec2:DescribeInstances." {
		t.Errorf("lines = %q", x.Lines)
	}
	if fact(x, "denied") != "no identity-based policy allows it" || fact(x, "action") != "ec2:DescribeInstances" {
		t.Errorf("facts = %v", x.Facts)
	}
}

func TestExplainDenyWithoutPolicyARN(t *testing.T) {
	err := apiError("SSM", "DescribeInstanceInformation", "AccessDeniedException",
		"User: arn:aws:iam::123456789012:user/John is not authorized to perform: ssm:DescribeInstanceInformation with an explicit deny in a permissions boundary")
	x := Error(err, "")
	if x.Headline != "No SSM access" || x.Lines[0] != "ssm:DescribeInstanceInformation is blocked by a permissions boundary." {
		t.Errorf("headline = %q lines = %q", x.Headline, x.Lines)
	}
	if strings.Contains(x.Lines[1], "region") {
		t.Errorf("only organization policies hint at regions, got %q", x.Lines[1])
	}
}

func TestExplainDenyInUnknownFormat(t *testing.T) {
	x := Error(apiError("SSM", "DescribeInstanceInformation", "AccessDeniedException", "Access denied"), "eu-west-1")
	if x.Status != "no SSM access in eu-west-1" || x.Lines[0] != "ssm:DescribeInstanceInformation was denied." {
		t.Errorf("status = %q lines = %q", x.Status, x.Lines)
	}
}

func TestExplainRegionNotEnabled(t *testing.T) {
	x := Error(apiError("SSM", "DescribeInstanceInformation", "UnrecognizedClientException", "The security token included in the request is invalid."), "af-south-1")
	if x.Headline != "AWS rejected the credentials in af-south-1" || !strings.Contains(x.Lines[0], "opt-in region") {
		t.Errorf("headline = %q lines = %q", x.Headline, x.Lines)
	}
}

func TestExplainUnknownRegionHost(t *testing.T) {
	dns := &net.DNSError{Err: "no such host", Name: "ssm.eu-north-9.amazonaws.com", IsNotFound: true}
	err := &smithy.OperationError{ServiceID: "SSM", OperationName: "DescribeInstanceInformation",
		Err: fmt.Errorf("send request: %w", &net.OpError{Op: "dial", Net: "tcp", Err: dns})}
	x := Error(err, "eu-north-9")
	if x.Headline != "Can't reach SSM in eu-north-9" || !strings.Contains(x.Lines[0], "ssm.eu-north-9.amazonaws.com") {
		t.Errorf("headline = %q lines = %q", x.Headline, x.Lines)
	}
}

func TestExplainTimeout(t *testing.T) {
	err := &smithy.OperationError{ServiceID: "SSM", OperationName: "DescribeInstanceInformation", Err: context.DeadlineExceeded}
	if x := Error(err, "us-east-1"); x.Status != "SSM timed out in us-east-1" {
		t.Errorf("status = %q", x.Status)
	}
}

func TestExplainThrottled(t *testing.T) {
	x := Error(apiError("EC2", "DescribeInstances", "RequestLimitExceeded", "Request limit exceeded."), "us-east-1")
	if x.Status != "throttled by EC2 in us-east-1" {
		t.Errorf("status = %q", x.Status)
	}
}

func TestExplainSSOExpired(t *testing.T) {
	x := Error(errors.New("failed to refresh cached credentials, the SSO session has expired or is invalid"), "us-east-1")
	if x.Headline != "AWS SSO session expired" || !x.SignIn {
		t.Errorf("headline = %q signIn = %v", x.Headline, x.SignIn)
	}
}

func TestExplainOtherErrors(t *testing.T) {
	x := Error(apiError("SSM", "DescribeInstanceInformation", "ValidationException", "1 validation error detected"), "us-east-1")
	if x.Status != "SSM DescribeInstanceInformation failed in us-east-1 (ValidationException)" || x.Lines[0] != "1 validation error detected" {
		t.Errorf("status = %q lines = %q", x.Status, x.Lines)
	}
	plain := Error(errors.New("something odd"), "")
	if plain.Headline != "AWS request failed" || plain.Lines[0] != "something odd" {
		t.Errorf("headline = %q lines = %q", plain.Headline, plain.Lines)
	}
}

func TestExplainMissingRegion(t *testing.T) {
	err := &smithy.OperationError{ServiceID: "SSM", OperationName: "DescribeInstanceInformation", Err: &aws.MissingRegionError{}}
	if x := Error(err, ""); x.Headline != "No AWS region set" || x.SignIn {
		t.Errorf("headline = %q signIn = %v", x.Headline, x.SignIn)
	}
}

// credentialError wraps cause the way the SDK does when a call fails before it
// is sent, while getting credentials.
func credentialError(cause error) error {
	return &smithy.OperationError{ServiceID: "SSM", OperationName: "DescribeInstanceInformation",
		Err: fmt.Errorf("get identity: get credentials: failed to refresh cached credentials, %w", cause)}
}

// TestExplainCredentialFailures uses error texts the SDK returned in real runs
// against a scratch AWS config, which need no network access to AWS.
func TestExplainCredentialFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		headline string
	}{
		"no credentials": {credentialError(errors.New(`no EC2 IMDS role found, operation error ec2imds: GetMetadata, request canceled, context deadline exceeded`)),
			"No AWS credentials found"},
		"credential_process": {credentialError(errors.New("process provider error: error in credential_process: exit status 1")),
			"The profile's credential_process failed"},
		"no SSO role": {credentialError(apiError("SSO", "GetRoleCredentials", "ForbiddenException", "No access")),
			"No access to the profile's SSO role"},
		"role not assumable": {credentialError(apiError("STS", "AssumeRole", "AccessDenied",
			"User: arn:aws:iam::123456789012:user/jane is not authorized to perform: sts:AssumeRole on resource: arn:aws:iam::123456789012:role/Admin")),
			"Can't assume the profile's role"},
		"missing region": {&smithy.OperationError{ServiceID: "SSM", OperationName: "DescribeInstanceInformation",
			Err: errors.New("failed to resolve service endpoint, endpoint rule error, Invalid Configuration: Missing Region")},
			"No AWS region set"},
		"profile missing": {config.SharedConfigProfileNotExistError{Profile: "nope"},
			"Profile nope isn't in your AWS config"},
	} {
		if x := Error(tc.err, "us-east-1"); x.Headline != tc.headline || x.SignIn {
			t.Errorf("%s: headline = %q signIn = %v, want %q", name, x.Headline, x.SignIn, tc.headline)
		}
	}
}

func TestExplainUsesTheCallThatFailed(t *testing.T) {
	x := Error(credentialError(apiError("STS", "AssumeRole", "AccessDenied",
		"User: arn:aws:iam::123456789012:user/jane is not authorized to perform: sts:AssumeRole on resource: arn:aws:iam::123456789012:role/Admin because no identity-based policy allows the sts:AssumeRole action")), "us-east-1")
	if x.Service != "STS" || fact(x, "action") != "sts:AssumeRole" || !strings.Contains(x.Lines[0], "sts:AssumeRole") {
		t.Errorf("the denial is STS's, not SSM's: service = %q facts = %v lines = %q", x.Service, x.Facts, x.Lines)
	}
}

func TestExplainTargetNotConnected(t *testing.T) {
	err := &smithy.OperationError{ServiceID: "SSM", OperationName: "StartSession", Err: &smithy.GenericAPIError{Code: "TargetNotConnected", Message: "i-0abc is not connected."}}
	if x := Error(err, "us-east-1"); x.Status != "instance not connected to SSM" || x.Lines[0] != "i-0abc is not connected." {
		t.Errorf("status = %q lines = %q", x.Status, x.Lines)
	}
}

func TestExplainNonAWSErrorKeepsItsText(t *testing.T) {
	if x := Error(errors.New("exit status 255"), ""); x.Status != "exit status 255" {
		t.Errorf("status = %q", x.Status)
	}
}

func TestExplainInvalidRegion(t *testing.T) {
	err := &smithy.OperationError{ServiceID: "SSM", OperationName: "DescribeInstanceInformation",
		Err: errors.New("failed to bind endpoint params, invalid input region ha_ha")}
	if x := Error(err, "ha_ha"); x.Headline != "The region isn't an AWS region code" {
		t.Errorf("headline = %q", x.Headline)
	}
}
