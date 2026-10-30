package session

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
)

func TestCaptureStderrKeepsTail(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo starting >&2; echo 'An error occurred (TargetNotConnected) when calling the StartSession operation: i-0abc is not connected.' >&2; exit 254")
	tail := captureStderr(cmd)
	err := cmd.Run()
	if err == nil || !strings.Contains(tail.String(), "TargetNotConnected") {
		t.Fatalf("err = %v, tail = %q", err, tail.String())
	}
	var op *smithy.OperationError
	var api smithy.APIError
	got := CLIError("SSM", tail.String(), err)
	if !errors.As(got, &op) || op.OperationName != "StartSession" || !errors.As(got, &api) || api.ErrorCode() != "TargetNotConnected" || api.ErrorMessage() != "i-0abc is not connected." {
		t.Errorf("CLIError = %v", got)
	}
}

func TestOutputTailIsBounded(t *testing.T) {
	tail := &outputTail{}
	_, _ = tail.Write([]byte(strings.Repeat("x", tailSize)))
	_, _ = tail.Write([]byte("end"))
	if s := tail.String(); len(s) != tailSize || !strings.HasSuffix(s, "end") {
		t.Errorf("tail should keep the last %d bytes, got %d ending %q", tailSize, len(s), s[len(s)-3:])
	}
}

func TestCLIErrorFallbacks(t *testing.T) {
	exit := errors.New("exit status 255")
	withRetries := "An error occurred (ThrottlingException) when calling the StartSession operation (reached max retries: 2): Rate exceeded"
	if got := CLIError("SSM", withRetries, exit); !strings.Contains(got.Error(), "ThrottlingException") {
		t.Errorf("retried errors should parse too, got %v", got)
	}
	if got := CLIError("SSO", "Attempting to open your browser\n\nError when retrieving token from sso: Token has expired and refresh failed\n", exit); got.Error() != "Error when retrieving token from sso: Token has expired and refresh failed" {
		t.Errorf("without a service error the last line should do, got %v", got)
	}
	if got := CLIError("SSM", "", exit); !errors.Is(got, exit) {
		t.Errorf("with no output the exit status is all there is, got %v", got)
	}
}
