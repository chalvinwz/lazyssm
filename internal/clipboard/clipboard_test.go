package clipboard

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// fakeHost swaps the platform seams for the duration of a test.
func fakeHost(t *testing.T, os string, env map[string]string, onPath map[string]string) {
	t.Helper()
	origGOOS, origGetenv, origLookPath := goos, getenv, lookPath
	t.Cleanup(func() { goos, getenv, lookPath = origGOOS, origGetenv, origLookPath })
	goos = os
	getenv = func(k string) string { return env[k] }
	lookPath = func(name string) (string, error) {
		if p, ok := onPath[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

func TestNativeTool(t *testing.T) {
	for _, tc := range []struct {
		name   string
		goos   string
		env    map[string]string
		onPath map[string]string
		want   []string
	}{
		{
			name:   "macOS local",
			goos:   "darwin",
			onPath: map[string]string{"pbcopy": "/usr/bin/pbcopy"},
			want:   []string{"/usr/bin/pbcopy"},
		},
		{
			name:   "macOS over SSH uses the terminal",
			goos:   "darwin",
			env:    map[string]string{"SSH_CONNECTION": "10.0.0.1 50000 10.0.0.2 22"},
			onPath: map[string]string{"pbcopy": "/usr/bin/pbcopy"},
		},
		{
			name:   "SSH_TTY alone also means remote",
			goos:   "darwin",
			env:    map[string]string{"SSH_TTY": "/dev/ttys003"},
			onPath: map[string]string{"pbcopy": "/usr/bin/pbcopy"},
		},
		{
			name: "macOS without pbcopy",
			goos: "darwin",
		},
		{
			name:   "Windows",
			goos:   "windows",
			onPath: map[string]string{"clip.exe": `C:\Windows\System32\clip.exe`},
			want:   []string{`C:\Windows\System32\clip.exe`},
		},
		{
			name: "WSL prefers the Windows clipboard over WSLg Wayland",
			goos: "linux",
			env:  map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WAYLAND_DISPLAY": "wayland-0"},
			onPath: map[string]string{
				"clip.exe": "/mnt/c/Windows/System32/clip.exe",
				"wl-copy":  "/usr/bin/wl-copy",
			},
			want: []string{"/mnt/c/Windows/System32/clip.exe"},
		},
		{
			name:   "Wayland",
			goos:   "linux",
			env:    map[string]string{"WAYLAND_DISPLAY": "wayland-0"},
			onPath: map[string]string{"wl-copy": "/usr/bin/wl-copy"},
			want:   []string{"/usr/bin/wl-copy"},
		},
		{
			name:   "X11 with xclip",
			goos:   "linux",
			env:    map[string]string{"DISPLAY": ":0"},
			onPath: map[string]string{"xclip": "/usr/bin/xclip", "xsel": "/usr/bin/xsel"},
			want:   []string{"/usr/bin/xclip", "-selection", "clipboard"},
		},
		{
			name:   "X11 with only xsel",
			goos:   "linux",
			env:    map[string]string{"DISPLAY": ":0"},
			onPath: map[string]string{"xsel": "/usr/bin/xsel"},
			want:   []string{"/usr/bin/xsel", "--clipboard", "--input"},
		},
		{
			name:   "Wayland session without wl-copy falls back to XWayland xclip",
			goos:   "linux",
			env:    map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			onPath: map[string]string{"xclip": "/usr/bin/xclip"},
			want:   []string{"/usr/bin/xclip", "-selection", "clipboard"},
		},
		{
			name:   "headless Linux ignores installed X tools",
			goos:   "linux",
			onPath: map[string]string{"xclip": "/usr/bin/xclip", "wl-copy": "/usr/bin/wl-copy"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeHost(t, tc.goos, tc.env, tc.onPath)
			if got := nativeTool(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("nativeTool() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeTool writes an executable stand-in for a copy tool that records its
// stdin to <dir>/copied and exits with the given status.
func fakeTool(t *testing.T, exit int) (path, copied string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake copy tool is a shell script")
	}
	// Starting a just-written script can take seconds while the whole suite
	// runs in parallel; that is not a wedged tool, so lift the 3 s limit.
	origTimeout := toolTimeout
	toolTimeout = 30 * time.Second
	t.Cleanup(func() { toolTimeout = origTimeout })
	dir := t.TempDir()
	copied = filepath.Join(dir, "copied")
	path = filepath.Join(dir, "pbcopy")
	script := "#!/bin/sh\ncat > '" + copied + "'\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, copied
}

func TestCopyPipesExactTextToTool(t *testing.T) {
	tool, copied := fakeTool(t, 0)
	fakeHost(t, "darwin", nil, map[string]string{"pbcopy": tool})

	cmd, viaTerminal := Copy("instance ID", "i-0a2539774a1136205")
	if viaTerminal {
		t.Fatal("local macOS with pbcopy must not use the terminal clipboard")
	}
	out := cmd()
	msg, ok := out.(CopiedMsg)
	if !ok {
		t.Fatalf("want CopiedMsg, got %T", out)
	}
	if msg.Err != nil || msg.Label != "instance ID" || msg.Text != "i-0a2539774a1136205" {
		t.Errorf("unexpected msg: %+v", msg)
	}
	got, err := os.ReadFile(copied)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "i-0a2539774a1136205" {
		t.Errorf("tool received %q, want the bare ID with no newline", got)
	}
}

func TestCopyReportsToolFailure(t *testing.T) {
	tool, _ := fakeTool(t, 3)
	fakeHost(t, "darwin", nil, map[string]string{"pbcopy": tool})

	cmd, _ := Copy("IP", "10.2.200.145")
	if msg := cmd().(CopiedMsg); msg.Err == nil {
		t.Error("a tool exiting non-zero must surface as an error")
	}
}

func TestCopyOverSSHUsesOnlyTheTerminal(t *testing.T) {
	tool, copied := fakeTool(t, 0)
	fakeHost(t, "darwin", map[string]string{"SSH_CONNECTION": "x"}, map[string]string{"pbcopy": tool})

	cmd, viaTerminal := Copy("instance ID", "i-1")
	if !viaTerminal || cmd == nil {
		t.Fatalf("over SSH want a terminal clipboard cmd, got viaTerminal=%v cmd=%v", viaTerminal, cmd != nil)
	}
	if _, isNative := cmd().(CopiedMsg); isNative {
		t.Error("terminal path must not report a native copy")
	}
	if _, err := os.Stat(copied); !errors.Is(err, os.ErrNotExist) {
		t.Error("native tool ran alongside the terminal clipboard: the copy would be duplicated")
	}
}
