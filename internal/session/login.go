package session

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// LoginEndedMsg is delivered to the TUI when `aws sso login` exits.
type LoginEndedMsg struct {
	Err error
}

// buildLoginArgs assembles the `aws sso login` arguments, threading the active
// profile/region exactly like buildArgs does for start-session.
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

// LoginCmd returns a tea.Cmd that suspends the TUI, runs `aws sso login` with
// the terminal attached (the browser flow prints to it and may block on
// input), and emits a LoginEndedMsg when the login exits (restoring the TUI).
func LoginCmd(profile, region string) tea.Cmd {
	cmd := exec.Command("aws", buildLoginArgs(profile, region)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return LoginEndedMsg{Err: err}
	})
}
