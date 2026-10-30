package session

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// LoginEndedMsg is delivered to the TUI when `aws sso login` exits.
type LoginEndedMsg struct {
	Err    error
	Output string // the tail of what the CLI wrote to stderr
}

// buildLoginArgs returns the `aws sso login` arguments, passing the active
// profile and region the same way buildArgs does.
func buildLoginArgs(profile, region string) []string {
	args := []string{"sso", "login"}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	if region != "" {
		args = append(args, "--region", region)
	}
	return args
}

// LoginCmd returns a tea.Cmd that suspends the TUI, runs `aws sso login`, and
// emits a LoginEndedMsg when it exits and the TUI resumes. The terminal stays
// attached because the browser flow prints to it and may wait for input.
func LoginCmd(profile, region string) tea.Cmd {
	cmd := exec.Command("aws", buildLoginArgs(profile, region)...)
	tail := captureStderr(cmd)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return LoginEndedMsg{Err: err, Output: tail.String()}
	})
}
