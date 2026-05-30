// Package session starts interactive SSM sessions by shelling out to the AWS
// CLI, handing the terminal over to the child process via Bubble Tea.
package session

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// SessionEndedMsg is delivered to the TUI when an SSM session exits.
type SessionEndedMsg struct {
	InstanceID string
	Err        error
}

// buildArgs assembles the `aws ssm start-session` arguments, threading the
// active profile/region so the session matches the browsed inventory.
func buildArgs(instanceID, profile, region string) []string {
	args := []string{"ssm", "start-session", "--target", instanceID}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	if region != "" {
		args = append(args, "--region", region)
	}
	return args
}

// ConnectCmd returns a tea.Cmd that suspends the TUI, runs
// `aws ssm start-session --target <instanceID>` with the terminal attached, and
// emits a SessionEndedMsg when the session exits (restoring the TUI).
func ConnectCmd(instanceID, profile, region string) tea.Cmd {
	cmd := exec.Command("aws", buildArgs(instanceID, profile, region)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return SessionEndedMsg{InstanceID: instanceID, Err: err}
	})
}
