package packaging

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	shTag     = "v9.9.9"
	shVersion = "9.9.9"

	// The install pages `lazyssm doctor` links; install.sh links the same.
	awsCLIDocURL = "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
	pluginDocURL = "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"
)

// shTools are the real commands on install.sh's hermetic PATH: those the
// script may call, minus curl and wget (fixtures are local, so nothing may be
// fetched over the network) and sudo (only ever faked), plus gzip, which GNU
// tar runs for -z.
var shTools = []string{
	"uname", "tar", "gzip", "mktemp", "rm", "mkdir", "cp", "install", "chmod",
	"grep", "awk", "sed", "sha256sum", "shasum", "id", "head", "tr",
}

// shRun runs install.sh under one shell with exactly env.
type shRun func(t *testing.T, env map[string]string) (stdout, stderr string, code int)

// shell is one shell install.sh is tested under.
type shell struct {
	run  shRun // `<shell> ../install.sh`
	pipe shRun // `<shell> -s` with the script on stdin, as `curl … | sh` runs it
}

// eachShell runs fn as a subtest per shell install.sh must work under:
// /bin/sh, and /bin/dash when present (Ubuntu's /bin/sh), so dash is
// exercised locally and not only in CI.
func eachShell(t *testing.T, fn func(t *testing.T, sh shell)) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux; Windows uses install.ps1")
	}
	t.Run("sh", func(t *testing.T) {
		fn(t, shell{run: runInstallSH, pipe: pipedRun("/bin/sh")})
	})
	if _, err := os.Stat("/bin/dash"); err == nil {
		t.Run("dash", func(t *testing.T) {
			fn(t, shell{run: scriptRun("/bin/dash"), pipe: pipedRun("/bin/dash")})
		})
	}
}

func scriptRun(shellPath string) shRun {
	return func(t *testing.T, env map[string]string) (string, string, int) {
		t.Helper()
		return runScript(t, env, nil, shellPath, installSH)
	}
}

func pipedRun(shellPath string) shRun {
	return func(t *testing.T, env map[string]string) (string, string, int) {
		t.Helper()
		script, err := os.ReadFile(installSH)
		if err != nil {
			t.Fatalf("read %s: %v", installSH, err)
		}
		return runScript(t, env, bytes.NewReader(script), shellPath, "-s")
	}
}

// sandbox is everything one install.sh run sees: a fake release, an install
// dir, an empty HOME and TMPDIR, a dir of fake commands, and the real tools.
type sandbox struct {
	root  string   // holds the dirs below; tests add their own here
	base  string   // LAZYSSM_DOWNLOAD_BASE
	dir   string   // LAZYSSM_INSTALL_DIR
	home  string   // HOME; must stay empty (no rc file edits)
	tmp   string   // TMPDIR; must be empty again after every run
	fakes string   // fake commands (uname, sudo, aws, ...)
	tools string   // links to the real shTools
	path  []string // PATH entries, in order
}

func newSandbox(t *testing.T, base string) *sandbox {
	t.Helper()
	root := t.TempDir()
	s := &sandbox{
		root:  root,
		base:  base,
		dir:   mkdir(t, root, "bin"),
		home:  mkdir(t, root, "home"),
		tmp:   mkdir(t, root, "tmp"),
		fakes: mkdir(t, root, "fakebin"),
		tools: toolPath(t, shTools...),
	}
	s.path = []string{s.fakes, s.dir, s.tools}
	return s
}

// newRelease is a sandbox serving a full fake release of shTag.
func newRelease(t *testing.T) *sandbox {
	t.Helper()
	return newSandbox(t, fakeRelease(t, shTag, releaseAssets(t, shVersion)))
}

func (s *sandbox) env(version string) map[string]string {
	return map[string]string{
		"PATH":                  strings.Join(s.path, ":"),
		"HOME":                  s.home,
		"TMPDIR":                s.tmp,
		"LAZYSSM_VERSION":       version,
		"LAZYSSM_INSTALL_DIR":   s.dir,
		"LAZYSSM_DOWNLOAD_BASE": s.base,
	}
}

// install runs install.sh for version and checks that, whatever the outcome,
// it removed its temp dir.
func (s *sandbox) install(t *testing.T, run shRun, version string) (stdout, stderr string, code int) {
	t.Helper()
	stdout, stderr, code = run(t, s.env(version))
	if left := entries(t, s.tmp); len(left) > 0 {
		t.Errorf("install.sh left %v behind in TMPDIR", left)
	}
	return stdout, stderr, code
}

// docFiles are the files a release archive carries next to the binary.
func docFiles() map[string]fileSpec {
	return map[string]fileSpec{
		"README.md":    {Body: []byte("# lazyssm\n"), Mode: 0o644},
		"LICENSE":      {Body: []byte("MIT License\n"), Mode: 0o644},
		"CHANGELOG.md": {Body: []byte("# Changelog\n"), Mode: 0o644},
	}
}

func tarName(version, goos, goarch string) string {
	return fmt.Sprintf("lazyssm_%s_%s_%s.tar.gz", version, goos, goarch)
}

// shArchive is a macOS/Linux release asset: the fake binary at the archive
// root next to the docs.
func shArchive(t *testing.T, version string) []byte {
	t.Helper()
	files := docFiles()
	files["lazyssm"] = fileSpec{Body: fakeBinarySH(version), Mode: 0o755}
	return tarGz(t, files)
}

// releaseAssets is a whole release: a tar.gz per macOS/Linux platform and a
// zip per Windows one, keyed by asset name.
func releaseAssets(t *testing.T, version string) map[string][]byte {
	t.Helper()
	archive := shArchive(t, version)
	winFiles := docFiles()
	winFiles["lazyssm.exe"] = fileSpec{Body: []byte("not a real exe\n"), Mode: 0o755}
	win := zipFile(t, winFiles)
	assets := map[string][]byte{}
	for _, arch := range []string{"amd64", "arm64"} {
		assets[tarName(version, "darwin", arch)] = archive
		assets[tarName(version, "linux", arch)] = archive
		assets[fmt.Sprintf("lazyssm_%s_windows_%s.zip", version, arch)] = win
	}
	return assets
}

// editChecksums rewrites each `<sum>  <name>` line of the shTag release's
// checksums.txt to what edit returns.
func editChecksums(t *testing.T, base string, edit func(sum, name string) string) {
	t.Helper()
	path := filepath.Join(base, shTag, "checksums.txt")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		out.WriteString(edit(sum, name) + "\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdir(t *testing.T, elem ...string) string {
	t.Helper()
	p := filepath.Join(elem...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	return p
}

// entries lists the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(des))
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

// readIfExists returns path's contents, or "" if there is no such file.
func readIfExists(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// writeFake writes an executable sh script named name into dir.
func writeFake(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

// shQuote quotes s as a single sh word.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fakeUname answers `uname -s` and `uname -m` as the given platform would.
func fakeUname(t *testing.T, dir, sys, machine string) {
	t.Helper()
	writeFake(t, dir, "uname", fmt.Sprintf(`case "$1" in
-s) echo %s ;;
-m) echo %s ;;
*) echo "fake uname: unexpected args: $*" >&2; exit 2 ;;
esac
`, shQuote(sys), shQuote(machine)))
}

// fakeSudo stands in for sudo: it appends its argv to log, then runs it with
// dir made writable for the duration, as it would be for root.
func fakeSudo(t *testing.T, fakes, log, dir string) {
	t.Helper()
	writeFake(t, fakes, "sudo", fmt.Sprintf(`printf '%%s\n' "$*" >> %[1]s
chmod u+w %[2]s || exit 1
"$@"
rc=$?
chmod 0555 %[2]s
exit "$rc"
`, shQuote(log), shQuote(dir)))
}

// makeReadOnly chmods dir 0555 until the test ends.
func makeReadOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	// Runs before t.TempDir's cleanup, which needs to delete what is inside.
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("chmod %s: %v", dir, err)
		}
	})
}

// skipIfRoot skips tests that need a dir the installer cannot write.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can write a 0555 dir, so the not-writable path cannot be exercised")
	}
}

// lineWith reports whether one line of text contains every sub.
func lineWith(text string, subs ...string) bool {
	for _, line := range strings.Split(text, "\n") {
		all := true
		for _, sub := range subs {
			all = all && strings.Contains(line, sub)
		}
		if all {
			return true
		}
	}
	return false
}

func wantSuccess(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("install.sh exited %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func wantFailure(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("install.sh exited 0, want a failure\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// wantInstalled checks that <dir>/lazyssm is mode 0755 and is version's binary.
func wantInstalled(t *testing.T, dir, version string) {
	t.Helper()
	bin := filepath.Join(dir, "lazyssm")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("installed binary: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("%s mode = %#o, want 0755", bin, got)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("run %s --version: %v", bin, err)
	}
	if got, want := strings.TrimSpace(string(out)), "lazyssm "+version; got != want {
		t.Errorf("%s --version = %q, want %q", bin, got, want)
	}
}

func wantNotInstalled(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "lazyssm")
	if _, err := os.Lstat(bin); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists (stat err %v); nothing should have been installed", bin, err)
	}
}

func TestInstallSHInstallsVerifiedBinary(t *testing.T) {
	for _, version := range []string{shTag, shVersion} {
		t.Run(version, func(t *testing.T) {
			eachShell(t, func(t *testing.T, sh shell) {
				s := newRelease(t)
				stdout, stderr, code := s.install(t, sh.run, version)
				wantSuccess(t, code, stdout, stderr)
				wantInstalled(t, s.dir, shVersion)
				if got := entries(t, s.dir); !slices.Equal(got, []string{"lazyssm"}) {
					t.Errorf("install dir holds %v, want only lazyssm (no README.md, LICENSE, ...)", got)
				}
				if got := entries(t, s.home); len(got) > 0 {
					t.Errorf("HOME gained %v; install.sh must not touch rc files", got)
				}
				for _, want := range []string{filepath.Join(s.dir, "lazyssm"), "lazyssm " + shVersion} {
					if !strings.Contains(stdout, want) {
						t.Errorf("stdout lacks %q:\n%s", want, stdout)
					}
				}
				if strings.Contains(stdout+stderr, "\x1b[") {
					t.Errorf("output has ANSI escapes though stdout is not a TTY:\n%q", stdout+stderr)
				}
			})
		})
	}
}

func TestInstallSHWarnsWhenDirNotOnPath(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		t.Run("off_path", func(t *testing.T) {
			s := newRelease(t)
			s.path = []string{s.fakes, s.tools}
			stdout, stderr, code := s.install(t, sh.run, shTag)
			wantSuccess(t, code, stdout, stderr)
			wantInstalled(t, s.dir, shVersion)
			if !lineWith(stderr, "warning:", s.dir) {
				t.Errorf("stderr has no warning naming %s:\n%s", s.dir, stderr)
			}
		})
		t.Run("on_path", func(t *testing.T) {
			s := newRelease(t)
			stdout, stderr, code := s.install(t, sh.run, shTag)
			wantSuccess(t, code, stdout, stderr)
			if lineWith(stderr, "warning:", s.dir) {
				t.Errorf("warned about %s though it is on PATH:\n%s", s.dir, stderr)
			}
		})
	})
}

func TestInstallSHRejectsChecksumMismatch(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newRelease(t)
		editChecksums(t, s.base, func(sum, name string) string {
			if strings.HasSuffix(name, ".tar.gz") {
				sum = fmt.Sprintf("%x", sha256.Sum256([]byte("tampered "+name)))
			}
			return sum + "  " + name
		})
		stdout, stderr, code := s.install(t, sh.run, shTag)
		wantFailure(t, code, stdout, stderr)
		if !strings.Contains(stderr, "checksum") {
			t.Errorf("stderr does not mention the checksum:\n%s", stderr)
		}
		wantNotInstalled(t, s.dir)
	})
}

func TestInstallSHRejectsMissingChecksumLine(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newRelease(t)
		// Each archive keeps its correct hash, but under a near-miss name, so
		// only an exact-name lookup turns it away.
		editChecksums(t, s.base, func(sum, name string) string {
			if strings.HasSuffix(name, ".tar.gz") {
				name += ".old"
			}
			return sum + "  " + name
		})
		stdout, stderr, code := s.install(t, sh.run, shTag)
		wantFailure(t, code, stdout, stderr)
		if !strings.Contains(stderr, "checksum") {
			t.Errorf("stderr does not mention the checksum:\n%s", stderr)
		}
		wantNotInstalled(t, s.dir)
	})
}

func TestInstallSHFailsWhenAssetMissing(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newRelease(t)
		archives, err := filepath.Glob(filepath.Join(s.base, shTag, "*.tar.gz"))
		if err != nil || len(archives) == 0 {
			t.Fatalf("glob archives: %v (found %d)", err, len(archives))
		}
		for _, a := range archives {
			if err := os.Remove(a); err != nil {
				t.Fatalf("remove %s: %v", a, err)
			}
		}
		stdout, stderr, code := s.install(t, sh.run, shTag)
		wantFailure(t, code, stdout, stderr)
		if !strings.Contains(stderr, "lazyssm_"+shVersion+"_") {
			t.Errorf("stderr does not name the missing archive:\n%s", stderr)
		}
		wantNotInstalled(t, s.dir)
	})
}

func TestInstallSHMapsPlatform(t *testing.T) {
	supported := []struct{ sys, machine, platform string }{
		{"Darwin", "arm64", "darwin_arm64"},
		{"Darwin", "x86_64", "darwin_amd64"},
		{"Linux", "x86_64", "linux_amd64"},
		{"Linux", "aarch64", "linux_arm64"},
	}
	for _, c := range supported {
		t.Run(c.sys+"_"+c.machine, func(t *testing.T) {
			eachShell(t, func(t *testing.T, sh shell) {
				// The release holds only the expected archive.
				asset := "lazyssm_" + shVersion + "_" + c.platform + ".tar.gz"
				s := newSandbox(t, fakeRelease(t, shTag, map[string][]byte{asset: shArchive(t, shVersion)}))
				fakeUname(t, s.fakes, c.sys, c.machine)
				stdout, stderr, code := s.install(t, sh.run, shTag)
				wantSuccess(t, code, stdout, stderr)
				wantInstalled(t, s.dir, shVersion)
			})
		})
	}

	rejected := []struct{ sys, machine, want string }{
		{"MINGW64_NT-10.0", "x86_64", "install.ps1"},
		{"Linux", "i686", "unsupported"},
	}
	for _, c := range rejected {
		t.Run(c.sys+"_"+c.machine, func(t *testing.T) {
			eachShell(t, func(t *testing.T, sh shell) {
				s := newRelease(t)
				fakeUname(t, s.fakes, c.sys, c.machine)
				stdout, stderr, code := s.install(t, sh.run, shTag)
				wantFailure(t, code, stdout, stderr)
				if !strings.Contains(stderr, c.want) {
					t.Errorf("stderr lacks %q:\n%s", c.want, stderr)
				}
				wantNotInstalled(t, s.dir)
			})
		})
	}
}

func TestInstallSHUsesSudoOnlyWhenNeeded(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		t.Run("writable", func(t *testing.T) {
			s := newRelease(t)
			log := filepath.Join(s.root, "sudo.log")
			fakeSudo(t, s.fakes, log, s.dir)
			stdout, stderr, code := s.install(t, sh.run, shTag)
			wantSuccess(t, code, stdout, stderr)
			wantInstalled(t, s.dir, shVersion)
			if got := readIfExists(t, log); got != "" {
				t.Errorf("sudo ran though %s is writable:\n%s", s.dir, got)
			}
		})
		t.Run("read_only", func(t *testing.T) {
			skipIfRoot(t)
			s := newRelease(t)
			log := filepath.Join(s.root, "sudo.log")
			fakeSudo(t, s.fakes, log, s.dir)
			makeReadOnly(t, s.dir)
			stdout, stderr, code := s.install(t, sh.run, shTag)
			wantSuccess(t, code, stdout, stderr)
			wantInstalled(t, s.dir, shVersion)
			got := readIfExists(t, log)
			if !lineWith(got, filepath.Join(s.dir, "lazyssm")) {
				t.Errorf("sudo log has no line naming %s/lazyssm:\n%s", s.dir, got)
			}
			for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
				if !strings.HasPrefix(line, "install ") && !strings.HasPrefix(line, "mkdir ") {
					t.Errorf("sudo ran %q; it may only create the dir or copy the binary", line)
				}
			}
		})
	})
}

func TestInstallSHNoSudoHint(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		skipIfRoot(t)
		s := newRelease(t)
		makeReadOnly(t, s.dir)
		// No fake sudo in s.fakes, and shTools never links the real one.
		stdout, stderr, code := s.install(t, sh.run, shTag)
		wantFailure(t, code, stdout, stderr)
		if !strings.Contains(stderr, "LAZYSSM_INSTALL_DIR") {
			t.Errorf("stderr lacks a LAZYSSM_INSTALL_DIR hint:\n%s", stderr)
		}
		wantNotInstalled(t, s.dir)
	})
}

func TestInstallSHWarnsMissingAWSTools(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		t.Run("missing", func(t *testing.T) {
			s := newRelease(t)
			stdout, stderr, code := s.install(t, sh.run, shTag)
			wantSuccess(t, code, stdout, stderr)
			for _, url := range []string{awsCLIDocURL, pluginDocURL} {
				if !strings.Contains(stdout+stderr, url) {
					t.Errorf("output lacks %s:\nstdout:\n%s\nstderr:\n%s", url, stdout, stderr)
				}
			}
		})
		t.Run("present", func(t *testing.T) {
			s := newRelease(t)
			writeFake(t, s.fakes, "aws", "exit 0\n")
			writeFake(t, s.fakes, "session-manager-plugin", "exit 0\n")
			stdout, stderr, code := s.install(t, sh.run, shTag)
			wantSuccess(t, code, stdout, stderr)
			for _, url := range []string{awsCLIDocURL, pluginDocURL} {
				if strings.Contains(stdout+stderr, url) {
					t.Errorf("output links %s though the tool is on PATH:\nstdout:\n%s\nstderr:\n%s", url, stdout, stderr)
				}
			}
		})
	})
}

func TestInstallSHWorksWhenPiped(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newRelease(t)
		stdout, stderr, code := s.install(t, sh.pipe, shTag)
		wantSuccess(t, code, stdout, stderr)
		wantInstalled(t, s.dir, shVersion)
	})
}

func TestInstallSHUpgradesExistingInstall(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newSandbox(t, fakeRelease(t, "v9.9.8", releaseAssets(t, "9.9.8")))
		stdout, stderr, code := s.install(t, sh.run, "v9.9.8")
		wantSuccess(t, code, stdout, stderr)
		wantInstalled(t, s.dir, "9.9.8")

		s.base = fakeRelease(t, shTag, releaseAssets(t, shVersion))
		stdout, stderr, code = s.install(t, sh.run, shTag)
		wantSuccess(t, code, stdout, stderr)
		if !strings.Contains(stdout, "lazyssm "+shVersion) {
			t.Errorf("second run does not print lazyssm %s:\n%s", shVersion, stdout)
		}
		wantInstalled(t, s.dir, shVersion)
	})
}

func TestInstallSHHandlesSpacesInDir(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newRelease(t)
		s.dir = filepath.Join(s.root, "my tools", "bin") // not created: install.sh must
		s.tmp = mkdir(t, s.root, "tmp dir")              // spaces in temp paths too
		s.path = []string{s.fakes, s.dir, s.tools}
		stdout, stderr, code := s.install(t, sh.run, shTag)
		wantSuccess(t, code, stdout, stderr)
		wantInstalled(t, s.dir, shVersion)
	})
}

func TestInstallSHWarnsAboutShadowingBinary(t *testing.T) {
	eachShell(t, func(t *testing.T, sh shell) {
		s := newRelease(t)
		shadow := mkdir(t, s.root, "gobin") // e.g. ~/go/bin from `go install`
		if err := os.WriteFile(filepath.Join(shadow, "lazyssm"), fakeBinarySH("0.0.1"), 0o755); err != nil {
			t.Fatalf("write shadowing lazyssm: %v", err)
		}
		s.path = []string{shadow, s.fakes, s.dir, s.tools}
		stdout, stderr, code := s.install(t, sh.run, shTag)
		wantSuccess(t, code, stdout, stderr)
		wantInstalled(t, s.dir, shVersion)
		if !lineWith(stderr, "warning:", shadow) {
			t.Errorf("stderr has no warning naming %s:\n%s", shadow, stderr)
		}
	})
}
