package session

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/aws/smithy-go"
)

// tailSize bounds how much of a child's error output is kept.
const tailSize = 4096

// outputTail keeps the last bytes a child process writes to stderr, so lazyssm
// can say why it failed after the TUI takes the screen back.
type outputTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *outputTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailSize {
		t.buf = t.buf[len(t.buf)-tailSize:]
	}
	return len(p), nil
}

func (t *outputTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// captureStderr sends cmd's stderr to the terminal as usual and also keeps its
// tail. Bubble Tea does not replace a stderr that is already set.
func captureStderr(cmd *exec.Cmd) *outputTail {
	tail := &outputTail{}
	cmd.Stderr = io.MultiWriter(os.Stderr, tail)
	return tail
}

// cliErrorRE matches how the AWS CLI reports a service error, e.g.
// "An error occurred (TargetNotConnected) when calling the StartSession
// operation: i-0abc is not connected."
var cliErrorRE = regexp.MustCompile(`An error occurred \(([^)]+)\) when calling the (\w+) operation(?: \([^)]*\))?: (.*)`)

// CLIError turns what a failed aws command printed into an error lazyssm can
// explain: the service error the CLI reported, else its last line of output,
// else err itself (an exit status).
func CLIError(service, output string, err error) error {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if m := cliErrorRE.FindStringSubmatch(lines[i]); m != nil {
			return &smithy.OperationError{
				ServiceID:     service,
				OperationName: m[2],
				Err:           &smithy.GenericAPIError{Code: m[1], Message: strings.TrimSpace(m[3])},
			}
		}
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return errors.New(last)
	}
	return err
}
