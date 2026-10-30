// Package clipboard copies short text to the user's clipboard through exactly
// one mechanism per copy, so clipboard-history managers record a single entry.
package clipboard

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// toolTimeout bounds a copy-tool run so a wedged xclip cannot hang the command.
// It is a variable so tests can allow for slow start-up on a loaded host.
var toolTimeout = 3 * time.Second

// CopiedMsg reports the outcome of a copy made with the platform's copy tool.
type CopiedMsg struct {
	Label string // what was copied, e.g. "instance ID"
	Text  string
	Err   error
}

// Copy returns a command that places text on the clipboard with exactly one
// mechanism: the platform copy tool when one is usable, otherwise the
// terminal's OSC 52 clipboard. viaTerminal reports the latter; the terminal
// never acknowledges OSC 52, so no CopiedMsg follows in that case.
func Copy(label, text string) (cmd tea.Cmd, viaTerminal bool) {
	argv := nativeTool()
	if argv == nil {
		return tea.SetClipboard(text), true
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
		defer cancel()
		c := exec.CommandContext(ctx, argv[0], argv[1:]...)
		c.Stdin = strings.NewReader(text)
		return CopiedMsg{Label: label, Text: text, Err: c.Run()}
	}, false
}

// Host seams, swapped by tests so tool selection is testable on any platform.
var (
	goos     = runtime.GOOS
	getenv   = os.Getenv
	lookPath = exec.LookPath
)

// nativeTool returns the argv of the platform copy tool, or nil when the
// terminal's OSC 52 clipboard must be used instead: over SSH a local tool would
// fill the remote host's clipboard, and without a tool there is nothing to run.
func nativeTool() []string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil
	}
	var candidates [][]string
	switch goos {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip.exe"}}
	default:
		// WSL first: wl-copy under WSLg only reaches the Linux-side clipboard.
		if getenv("WSL_DISTRO_NAME") != "" {
			candidates = append(candidates, []string{"clip.exe"})
		}
		if getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-copy"})
		}
		if getenv("DISPLAY") != "" {
			candidates = append(candidates,
				[]string{"xclip", "-selection", "clipboard"},
				[]string{"xsel", "--clipboard", "--input"})
		}
	}
	for _, c := range candidates {
		if path, err := lookPath(c[0]); err == nil {
			return append([]string{path}, c[1:]...)
		}
	}
	return nil
}
