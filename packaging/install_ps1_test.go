package packaging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The install.ps1 tests run only on Windows, under Windows PowerShell 5.1
// (`powershell`) and, when it is on PATH, PowerShell 7 (`pwsh`). Elsewhere
// they skip, except TestInstallPS1StaysPortable, which only reads the script.
// This file builds on every OS.
//
// install.ps1 edits the real user Path (HKCU\Environment), so every test that
// runs it saves the raw value first and puts it back when the test ends.

const (
	// installPS1 is the script under test, relative to this package's directory.
	installPS1 = "../install.ps1"

	// psTag is the release most tests install. It is shTag because
	// editChecksums edits that release's checksums.txt.
	psTag     = shTag
	psVersion = shVersion

	// iexCommand runs install.ps1 the way `irm <url> | iex` does: the whole
	// script as one string, piped into Invoke-Expression.
	iexCommand = "Get-Content " + installPS1 + " -Raw | Invoke-Expression"
)

// eachPowerShell runs fn as a subtest per PowerShell install.ps1 must work
// under: Windows PowerShell 5.1, the Windows default, and PowerShell 7 when
// pwsh is on PATH.
func eachPowerShell(t *testing.T, fn func(t *testing.T, shell string)) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("install.ps1 is for Windows; macOS and Linux use install.sh")
	}
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Fatalf("Windows PowerShell not found: %v", err)
	}
	t.Run("powershell", func(t *testing.T) { fn(t, "powershell") })
	if _, err := exec.LookPath("pwsh"); err == nil {
		t.Run("pwsh", func(t *testing.T) { fn(t, "pwsh") })
	}
}

// runPS1 runs command under shell (powershell or pwsh) with exactly env, and
// returns its stdout and stderr together.
func runPS1(t *testing.T, shell string, env map[string]string, command string) (out string, code int) {
	t.Helper()
	exe, err := exec.LookPath(shell)
	if err != nil {
		t.Fatalf("%s not found: %v", shell, err)
	}
	stdout, stderr, code := runScript(t, env, nil, exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	return stdout + stderr, code
}

// runInstallPS1 runs `<shell> -NoProfile -Command "Get-Content ../install.ps1
// -Raw | Invoke-Expression"`, which is what `irm <url> | iex` does.
func runInstallPS1(t *testing.T, shell string, env map[string]string) (out string, code int) {
	t.Helper()
	return runPS1(t, shell, env, iexCommand)
}

// psEnv is this process's environment for a PowerShell child, minus
// PSModulePath (pwsh rewrites it, and Windows PowerShell must not pick up
// PowerShell 7's modules) and any LAZYSSM_* setting of the developer's own.
func psEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" { // Windows' per-drive "=C:=C:\..." entries
			continue
		}
		if strings.EqualFold(k, "PSModulePath") || strings.HasPrefix(strings.ToUpper(k), "LAZYSSM_") {
			continue
		}
		env[k] = v
	}
	return env
}

// setEnv sets k in env, replacing it under any spelling: Windows variable
// names ignore case.
func setEnv(env map[string]string, k, v string) {
	delEnv(env, k)
	env[k] = v
}

func delEnv(env map[string]string, k string) {
	for name := range env {
		if strings.EqualFold(name, k) {
			delete(env, name)
		}
	}
}

func envList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for _, k := range slices.Sorted(maps.Keys(env)) {
		list = append(list, k+"="+env[k])
	}
	return list
}

var (
	fakeEXEMu sync.Mutex
	fakeEXEs  = map[string][]byte{} // by version; each build takes a second or two
)

// fakeBinaryEXE is a stand-in lazyssm.exe: a real Windows program, built from
// a temp main.go, that prints what `lazyssm --version` prints.
func fakeBinaryEXE(t *testing.T, version string) []byte {
	t.Helper()
	fakeEXEMu.Lock()
	defer fakeEXEMu.Unlock()
	if exe, ok := fakeEXEs[version]; ok {
		return exe
	}
	goTool, err := exec.LookPath("go") // go test puts its own go first on PATH
	if err != nil {
		t.Fatalf("go not found: %v", err)
	}
	src := t.TempDir()
	files := map[string]string{
		"go.mod":  "module fakelazyssm\n\ngo 1.21\n",
		"main.go": fmt.Sprintf("package main\n\nimport \"os\"\n\nfunc main() { _, _ = os.Stdout.WriteString(%q) }\n", "lazyssm "+version+"\n"),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	out := filepath.Join(src, "lazyssm.exe")
	cmd := exec.CommandContext(t.Context(), goTool, "build", "-trimpath", "-o", out, ".")
	cmd.Dir = src
	// Offline and self-contained: no toolchain download, no workspace, no
	// GOFLAGS such as -mod=vendor from the caller.
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0",
		"GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake lazyssm.exe: %v\n%s", err, msg)
	}
	exe, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read fake lazyssm.exe: %v", err)
	}
	fakeEXEs[version] = exe
	return exe
}

func zipName(version, goarch string) string {
	return fmt.Sprintf("lazyssm_%s_windows_%s.zip", version, goarch)
}

// psArchive is a Windows release asset: the fake lazyssm.exe at the archive
// root next to the docs.
func psArchive(t *testing.T, version string) []byte {
	t.Helper()
	files := docFiles()
	files["lazyssm.exe"] = fileSpec{Body: fakeBinaryEXE(t, version), Mode: 0o755}
	return zipFile(t, files)
}

// psRelease serves both Windows zips of version, and returns the base for
// LAZYSSM_DOWNLOAD_BASE. Both hold a binary for this machine, so which one
// the installer picks only matters to TestInstallPS1MapsArch.
func psRelease(t *testing.T, version string) string {
	t.Helper()
	archive := psArchive(t, version)
	return fakeRelease(t, "v"+version, map[string][]byte{
		zipName(version, "amd64"): archive,
		zipName(version, "arm64"): archive,
	})
}

// psBox is everything one install.ps1 run sees.
type psBox struct {
	shell string            // powershell or pwsh
	root  string            // holds the dirs below; tests add their own here
	base  string            // LAZYSSM_DOWNLOAD_BASE
	dir   string            // LAZYSSM_INSTALL_DIR, not created up front; "" leaves it unset
	tmp   string            // TEMP and TMP; no lazyssm-* may be left in it after a run
	env   map[string]string // the rest of the environment; tests may edit it
	path  string            // the raw user Path before the test, restored after it
}

func newPSBox(t *testing.T, shell, base string) *psBox {
	t.Helper()
	root := t.TempDir()
	b := &psBox{
		shell: shell,
		root:  root,
		base:  base,
		dir:   filepath.Join(root, "lazyssm"),
		tmp:   mkdir(t, root, "tmp"),
		env:   psEnv(),
	}
	setEnv(b.env, "TEMP", b.tmp)
	setEnv(b.env, "TMP", b.tmp)
	b.path = keepUserPath(t)
	return b
}

func (b *psBox) vars(version string) map[string]string {
	env := maps.Clone(b.env)
	setEnv(env, "LAZYSSM_VERSION", version)
	setEnv(env, "LAZYSSM_DOWNLOAD_BASE", b.base)
	if b.dir != "" {
		setEnv(env, "LAZYSSM_INSTALL_DIR", b.dir)
	}
	return env
}

// install runs install.ps1 for version, as `irm | iex` would.
func (b *psBox) install(t *testing.T, version string) (out string, code int) {
	t.Helper()
	out, code = runInstallPS1(t, b.shell, b.vars(version))
	b.checkTemp(t)
	return out, code
}

// run runs command, which runs install.ps1 somehow, with the box's
// environment.
func (b *psBox) run(t *testing.T, version, command string) (out string, code int) {
	t.Helper()
	out, code = runPS1(t, b.shell, b.vars(version), command)
	b.checkTemp(t)
	return out, code
}

// checkTemp checks that install.ps1 removed its temp dir, whatever the
// outcome of the run.
func (b *psBox) checkTemp(t *testing.T) {
	t.Helper()
	for _, name := range entries(t, b.tmp) {
		if strings.HasPrefix(strings.ToLower(name), "lazyssm") {
			t.Errorf("install.ps1 left %s behind in TEMP", name)
		}
	}
}

// psQuery runs a short snippet in Windows PowerShell and returns its trimmed
// output. It does not use t.Context, so cleanups can call it.
func psQuery(script string) (string, error) {
	exe, err := exec.LookPath("powershell")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = envList(psEnv())
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%w: %s", err, exitErr.Stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// The registry snippets move values as base64 UTF-8, so console code pages
// never touch a Path with non-ASCII characters in it.
const readRawUserPath = `$k = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment'); ` +
	`if ($null -eq $k -or @($k.GetValueNames()) -notcontains 'Path') { 'absent' } ` +
	`else { $k.GetValueKind('Path').ToString() + ' ' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(` +
	`[string]$k.GetValue('Path', '', 'DoNotExpandEnvironmentNames'))) }`

func writeRawUserPath(raw, kind string) string {
	return fmt.Sprintf(`$k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment'); `+
		`$k.SetValue('Path', [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')), '%s'); $k.Close()`,
		base64.StdEncoding.EncodeToString([]byte(raw)), kind)
}

const deleteUserPath = `$k = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment'); ` +
	`$k.DeleteValue('Path', $false); $k.Close()`

// rawUserPath returns the user Path's registry type ("absent" when there is
// no Path value) and its raw value, with %VAR% references unexpanded.
func rawUserPath(t *testing.T) (kind, raw string) {
	t.Helper()
	got, err := psQuery(readRawUserPath)
	if err != nil {
		t.Fatalf("read user Path: %v", err)
	}
	if got == "absent" {
		return got, ""
	}
	kind, b64, _ := strings.Cut(got, " ")
	val, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode user Path %q: %v", b64, err)
	}
	return kind, string(val)
}

// keepUserPath saves the raw user Path, unexpanded and with its registry
// type, and restores exactly that when the test ends. It returns the raw value.
func keepUserPath(t *testing.T) (raw string) {
	t.Helper()
	kind, raw := rawUserPath(t)
	restore := deleteUserPath
	if kind != "absent" {
		if kind != "String" && kind != "ExpandString" {
			t.Fatalf("user Path has registry type %q; not touching it", kind)
		}
		restore = writeRawUserPath(raw, kind)
	}
	t.Cleanup(func() {
		if _, err := psQuery(restore); err != nil {
			t.Errorf("restore user Path: %v", err)
		}
	})
	return raw
}

// setUserPath stores raw as the user Path, as REG_EXPAND_SZ like Windows does.
func setUserPath(t *testing.T, raw string) {
	t.Helper()
	if _, err := psQuery(writeRawUserPath(raw, "ExpandString")); err != nil {
		t.Fatalf("set user Path: %v", err)
	}
}

// userPathEntries is the user Path as
// [Environment]::GetEnvironmentVariable('Path','User') returns it, split.
func userPathEntries(t *testing.T) []string {
	t.Helper()
	got, err := psQuery(`[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(` +
		`[string][Environment]::GetEnvironmentVariable('Path', 'User')))`)
	if err != nil {
		t.Fatalf("read user Path: %v", err)
	}
	val, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("decode user Path %q: %v", got, err)
	}
	return strings.Split(string(val), ";")
}

// pathCount counts the entries that name dir, ignoring case and trailing
// backslashes, as Windows does.
func pathCount(entries []string, dir string) int {
	want := strings.TrimRight(dir, `\`)
	n := 0
	for _, e := range entries {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(e), `\`), want) {
			n++
		}
	}
	return n
}

func wantPathOnce(t *testing.T, dir string) {
	t.Helper()
	entries := userPathEntries(t)
	if n := pathCount(entries, dir); n != 1 {
		t.Errorf("user Path names %s %d times, want once:\n%s", dir, n, strings.Join(entries, "\n"))
	}
}

func wantPSSuccess(t *testing.T, code int, out string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("install.ps1 exited %d, want 0\noutput:\n%s", code, out)
	}
}

func wantPSFailure(t *testing.T, code int, out string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("install.ps1 exited 0, want a failure\noutput:\n%s", out)
	}
}

// wantPSInstalled checks that <dir>\lazyssm.exe is version's binary.
func wantPSInstalled(t *testing.T, dir, version string) {
	t.Helper()
	bin := filepath.Join(dir, "lazyssm.exe")
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("run %s --version: %v", bin, err)
	}
	if got, want := strings.TrimSpace(string(out)), "lazyssm "+version; got != want {
		t.Errorf("%s --version = %q, want %q", bin, got, want)
	}
}

func wantPSNotInstalled(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "lazyssm.exe")
	if _, err := os.Lstat(bin); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists (stat err %v); nothing should have been installed", bin, err)
	}
	if n := pathCount(userPathEntries(t), dir); n != 0 {
		t.Errorf("user Path names %s though the install failed", dir)
	}
}

// tamperZipChecksums rewrites the psTag release's .zip lines in checksums.txt.
func tamperZipChecksums(t *testing.T, base string, edit func(sum, name string) (string, string)) {
	t.Helper()
	editChecksums(t, base, func(sum, name string) string {
		if strings.HasSuffix(name, ".zip") {
			sum, name = edit(sum, name)
		}
		return sum + "  " + name
	})
}

func TestInstallPS1InstallsVerifiedBinary(t *testing.T) {
	for _, version := range []string{psTag, psVersion} {
		t.Run(version, func(t *testing.T) {
			eachPowerShell(t, func(t *testing.T, shell string) {
				b := newPSBox(t, shell, psRelease(t, psVersion))
				out, code := b.install(t, version)
				wantPSSuccess(t, code, out)
				wantPSInstalled(t, b.dir, psVersion)
				if got := entries(t, b.dir); !slices.Equal(got, []string{"lazyssm.exe"}) {
					t.Errorf("install dir holds %v, want only lazyssm.exe (no README.md, LICENSE, ...)", got)
				}
				wantPathOnce(t, b.dir)
				for _, want := range []string{filepath.Join(b.dir, "lazyssm.exe"), "lazyssm " + psVersion} {
					if !strings.Contains(out, want) {
						t.Errorf("output lacks %q:\n%s", want, out)
					}
				}
			})
		})
	}
}

func TestInstallPS1RejectsChecksumMismatch(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, psRelease(t, psVersion))
		tamperZipChecksums(t, b.base, func(_, name string) (string, string) {
			return fmt.Sprintf("%x", sha256.Sum256([]byte("tampered "+name))), name
		})
		out, code := b.install(t, psTag)
		wantPSFailure(t, code, out)
		if !strings.Contains(out, "checksum") {
			t.Errorf("output does not mention the checksum:\n%s", out)
		}
		wantPSNotInstalled(t, b.dir)
	})
}

func TestInstallPS1RejectsMissingChecksumLine(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, psRelease(t, psVersion))
		// Each zip keeps its correct hash, but under a near-miss name, so
		// only an exact-name lookup turns it away.
		tamperZipChecksums(t, b.base, func(sum, name string) (string, string) {
			return sum, name + ".old"
		})
		out, code := b.install(t, psTag)
		wantPSFailure(t, code, out)
		if !strings.Contains(out, "checksum") {
			t.Errorf("output does not mention the checksum:\n%s", out)
		}
		wantPSNotInstalled(t, b.dir)
	})
}

func TestInstallPS1UpgradeKeepsSinglePathEntry(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, psRelease(t, "9.9.8"))
		out, code := b.install(t, "v9.9.8")
		wantPSSuccess(t, code, out)
		wantPSInstalled(t, b.dir, "9.9.8")
		wantPathOnce(t, b.dir)

		b.base = psRelease(t, psVersion)
		out, code = b.install(t, psTag)
		wantPSSuccess(t, code, out)
		if !strings.Contains(out, "lazyssm "+psVersion) {
			t.Errorf("second run does not print lazyssm %s:\n%s", psVersion, out)
		}
		wantPSInstalled(t, b.dir, psVersion)
		wantPathOnce(t, b.dir)
	})
}

func TestInstallPS1KeepsExistingPathEntry(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, psRelease(t, psVersion))
		// The dir is already there, in other case and with a trailing
		// backslash: Windows treats that as the same entry.
		variant := strings.ToUpper(b.dir) + `\`
		seeded := variant
		if b.path != "" {
			seeded = strings.TrimSuffix(b.path, ";") + ";" + variant
		}
		setUserPath(t, seeded)
		out, code := b.install(t, psTag)
		wantPSSuccess(t, code, out)
		wantPSInstalled(t, b.dir, psVersion)
		entries := userPathEntries(t)
		if n := pathCount(entries, b.dir); n != 1 {
			t.Errorf("user Path names %s %d times, want once:\n%s", b.dir, n, strings.Join(entries, "\n"))
		}
		if !slices.Contains(entries, variant) {
			t.Errorf("the existing entry %s was changed:\n%s", variant, strings.Join(entries, "\n"))
		}
	})
}

// TestInstallPS1KeepsUserPathRaw checks that adding the install dir leaves
// the rest of the user Path as it was: still REG_EXPAND_SZ, with %VAR%
// entries unexpanded. [Environment]::SetEnvironmentVariable would store the
// expanded value as REG_SZ.
func TestInstallPS1KeepsUserPathRaw(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, psRelease(t, psVersion))
		const sentinel = `%USERPROFILE%\lazyssm-sentinel`
		seeded := sentinel
		if b.path != "" {
			seeded = strings.TrimSuffix(b.path, ";") + ";" + sentinel
		}
		setUserPath(t, seeded)
		out, code := b.install(t, psTag)
		wantPSSuccess(t, code, out)
		wantPSInstalled(t, b.dir, psVersion)
		kind, raw := rawUserPath(t)
		if kind != "ExpandString" {
			t.Errorf("user Path registry type = %s, want ExpandString", kind)
		}
		entries := strings.Split(raw, ";")
		if !slices.Contains(entries, sentinel) {
			t.Errorf("user Path lost the unexpanded entry %s:\n%s", sentinel, strings.Join(entries, "\n"))
		}
		if n := pathCount(entries, b.dir); n != 1 {
			t.Errorf("user Path names %s %d times, want once:\n%s", b.dir, n, strings.Join(entries, "\n"))
		}
	})
}

// TestInstallPS1DoesNotCloseCallerShell runs a failing install the way a
// caller's session would, then keeps going. A failed install must raise an
// error the caller can catch, which prints `caught: <message>`; `exit`, or a
// bare `break`, would end the caller's whole session under iex, so `after`
// would never print.
func TestInstallPS1DoesNotCloseCallerShell(t *testing.T) {
	command := "try { " + iexCommand + " } catch { Write-Output ('caught: ' + $_.Exception.Message) }; Write-Output after"
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, fakeRelease(t, psTag, map[string][]byte{})) // checksums.txt, no zips
		out, _ := b.run(t, psTag, command)
		missing := "lazyssm_" + psVersion + "_windows_"
		if !strings.Contains(out, missing) {
			t.Errorf("output does not name the missing zip, so the failure path did not run:\n%s", out)
		}
		lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
		if !slices.ContainsFunc(lines, func(line string) bool {
			return strings.HasPrefix(line, "caught: ") && strings.Contains(line, missing)
		}) {
			t.Errorf("no `caught: ` line naming the missing zip, so no catchable error reached the caller:\n%s", out)
		}
		if !slices.Contains(lines, "after") {
			t.Errorf("the caller's session ended with the failed install; `after` never printed:\n%s", out)
		}
		wantPSNotInstalled(t, b.dir)
	})
}

func TestInstallPS1DefaultsToLocalAppData(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		b := newPSBox(t, shell, psRelease(t, psVersion))
		appData := mkdir(t, b.root, "appdata")
		setEnv(b.env, "LOCALAPPDATA", appData)
		b.dir = "" // leave LAZYSSM_INSTALL_DIR unset
		out, code := b.install(t, psTag)
		wantPSSuccess(t, code, out)
		dir := filepath.Join(appData, "Programs", "lazyssm")
		wantPSInstalled(t, dir, psVersion)
		wantPathOnce(t, dir)
	})
}

func TestInstallPS1MapsArch(t *testing.T) {
	supported := []struct{ arch, wow64, goarch string }{
		{"AMD64", "", "amd64"},
		{"ARM64", "", "arm64"},
		{"x86", "AMD64", "amd64"}, // 32-bit PowerShell on 64-bit Windows
	}
	for _, c := range supported {
		name := c.arch
		if c.wow64 != "" {
			name += "_on_" + c.wow64
		}
		t.Run(name, func(t *testing.T) {
			eachPowerShell(t, func(t *testing.T, shell string) {
				// The release holds only the expected zip.
				base := fakeRelease(t, psTag, map[string][]byte{zipName(psVersion, c.goarch): psArchive(t, psVersion)})
				b := newPSBox(t, shell, base)
				setEnv(b.env, "PROCESSOR_ARCHITECTURE", c.arch)
				delEnv(b.env, "PROCESSOR_ARCHITEW6432")
				if c.wow64 != "" {
					setEnv(b.env, "PROCESSOR_ARCHITEW6432", c.wow64)
				}
				out, code := b.install(t, psTag)
				wantPSSuccess(t, code, out)
				wantPSInstalled(t, b.dir, psVersion)
			})
		})
	}

	t.Run("x86_rejected", func(t *testing.T) {
		eachPowerShell(t, func(t *testing.T, shell string) {
			b := newPSBox(t, shell, psRelease(t, psVersion))
			setEnv(b.env, "PROCESSOR_ARCHITECTURE", "x86")
			delEnv(b.env, "PROCESSOR_ARCHITEW6432")
			out, code := b.install(t, psTag)
			wantPSFailure(t, code, out)
			if !strings.Contains(out, "unsupported architecture") {
				t.Errorf("output lacks %q:\n%s", "unsupported architecture", out)
			}
			wantPSNotInstalled(t, b.dir)
		})
	})
}

func TestInstallPS1WarnsMissingAWSTools(t *testing.T) {
	// A PATH with only Windows itself on it, so the runner's own aws and
	// session-manager-plugin are out of sight.
	minimalPath := func() string {
		root := os.Getenv("SystemRoot")
		return strings.Join([]string{
			filepath.Join(root, "System32"), root, filepath.Join(root, "System32", "WindowsPowerShell", "v1.0"),
		}, ";")
	}
	eachPowerShell(t, func(t *testing.T, shell string) {
		t.Run("missing", func(t *testing.T) {
			b := newPSBox(t, shell, psRelease(t, psVersion))
			setEnv(b.env, "PATH", minimalPath())
			out, code := b.install(t, psTag)
			wantPSSuccess(t, code, out)
			for _, url := range []string{awsCLIDocURL, pluginDocURL} {
				if !lineWith(out, "warning:", url) {
					t.Errorf("output has no warning linking %s:\n%s", url, out)
				}
			}
		})
		t.Run("present", func(t *testing.T) {
			b := newPSBox(t, shell, psRelease(t, psVersion))
			fakes := mkdir(t, b.root, "fakebin")
			for _, name := range []string{"aws.cmd", "session-manager-plugin.cmd"} {
				if err := os.WriteFile(filepath.Join(fakes, name), []byte("@exit /b 0\r\n"), 0o755); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			setEnv(b.env, "PATH", fakes+";"+minimalPath())
			out, code := b.install(t, psTag)
			wantPSSuccess(t, code, out)
			for _, url := range []string{awsCLIDocURL, pluginDocURL} {
				if strings.Contains(out, url) {
					t.Errorf("output links %s though the tool is on PATH:\n%s", url, out)
				}
			}
		})
	})
}

// exitStatement matches PowerShell's exit keyword used as a statement.
var exitStatement = regexp.MustCompile(`(?i)(^|[\s;{(])exit([\s;)]|$)`)

// TestInstallPS1StaysPortable checks, on every OS, what install.ps1 needs to
// run as `irm | iex` under Windows PowerShell 5.1: plain ASCII with no BOM
// (5.1 reads a BOM-less file in the ANSI code page, and its parser treats
// curly quotes as quotes); the whole body inside one `& { }` script block, so
// a truncated download fails to parse instead of running half a script; no
// exit, which would close the caller's PowerShell; and none of PowerShell 7's
// && || ?? ?. operators.
func TestInstallPS1StaysPortable(t *testing.T) {
	src, err := os.ReadFile(installPS1)
	if err != nil {
		t.Fatalf("read %s: %v", installPS1, err)
	}
	if bytes.HasPrefix(src, []byte("\xef\xbb\xbf")) {
		t.Errorf("%s starts with a UTF-8 BOM", installPS1)
	}
	var code []string // non-blank lines, comments removed
	for i, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSuffix(line, "\r")
		for _, r := range line {
			if r != '\t' && (r < 0x20 || r > 0x7e) {
				t.Errorf("line %d: %q is not plain ASCII", i+1, r)
				break
			}
		}
		if j := strings.Index(line, "#"); j >= 0 {
			line = line[:j]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		code = append(code, strings.TrimSpace(line))
		for _, op := range []string{"&&", "||", "??", "?."} {
			if strings.Contains(line, op) {
				t.Errorf("line %d uses %s, which Windows PowerShell 5.1 cannot parse", i+1, op)
			}
		}
		if exitStatement.MatchString(line) {
			t.Errorf("line %d calls exit, which would close the caller's PowerShell under iex", i+1)
		}
	}
	if len(code) == 0 {
		t.Fatalf("%s has no code", installPS1)
	}
	if first, last := code[0], code[len(code)-1]; first != "& {" || last != "}" {
		t.Errorf("the code must be one `& { ... }` script block; it starts %q and ends %q", first, last)
	}
}
