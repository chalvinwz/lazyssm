package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	formulaTmplPath = "../homebrew/lazyssm.rb.tmpl"
	scoopTmplPath   = "../scoop/lazyssm.json.tmpl"
)

var (
	hexA = strings.Repeat("a", 64)
	hexB = strings.Repeat("b", 64)
	hexC = strings.Repeat("c", 64)
	hexD = strings.Repeat("d", 64)
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// checksumsV030 is a checksums.txt in GoReleaser's format for v0.3.0 with
// darwin, linux and both windows assets.
func checksumsV030() string {
	return strings.Join([]string{
		hexD + "  lazyssm_0.3.0_darwin_amd64.tar.gz",
		hexD + "  lazyssm_0.3.0_darwin_arm64.tar.gz",
		hexD + "  lazyssm_0.3.0_linux_amd64.tar.gz",
		hexD + "  lazyssm_0.3.0_linux_arm64.tar.gz",
		hexA + "  lazyssm_0.3.0_windows_amd64.zip",
		hexB + "  lazyssm_0.3.0_windows_arm64.zip",
	}, "\n") + "\n"
}

type scoopArch struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

type scoopAutoupdateArch struct {
	URL string `json:"url"`
}

type scoopManifest struct {
	Version      string               `json:"version"`
	Description  string               `json:"description"`
	Homepage     string               `json:"homepage"`
	License      string               `json:"license"`
	Architecture map[string]scoopArch `json:"architecture"`
	Bin          string               `json:"bin"`
	Checkver     string               `json:"checkver"`
	Autoupdate   struct {
		Architecture map[string]scoopAutoupdateArch `json:"architecture"`
	} `json:"autoupdate"`
}

func decodeManifest(t *testing.T, out string) scoopManifest {
	t.Helper()
	var m scoopManifest
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, out)
	}
	return m
}

func TestFormulaRendersTagAndSHA(t *testing.T) {
	out, err := Formula(readFile(t, formulaTmplPath), "v0.3.0", hexC)
	if err != nil {
		t.Fatalf("Formula: %v", err)
	}
	for _, want := range []string{
		`class Lazyssm < Formula`,
		`desc "Terminal UI for AWS SSM Session Manager"`,
		`homepage "https://github.com/chalvinwz/lazyssm"`,
		`url "https://github.com/chalvinwz/lazyssm/archive/refs/tags/v0.3.0.tar.gz"`,
		`sha256 "` + hexC + `"`,
		`license "MIT"`,
		`head "https://github.com/chalvinwz/lazyssm.git", branch: "main"`,
		`depends_on "go" => :build`,
		`-X main.version=#{version}`,
		`./cmd/lazyssm`,
		`brew install awscli`,
		// Casks are macOS-only, so Linux gets AWS's install page instead.
		`if OS.mac?`,
		`brew install --cask session-manager-plugin`,
		`https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html`,
		`lazyssm doctor`,
		`assert_match "lazyssm #{version}", shell_output("#{bin}/lazyssm --version")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formula missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "{{") {
		t.Errorf("formula has unrendered template actions:\n%s", out)
	}

	ruby, err := exec.LookPath("ruby")
	if err != nil {
		// Every CI runner has ruby, so there the check must not go quiet.
		if os.Getenv("CI") != "" {
			t.Fatal("ruby not on PATH; CI must run the ruby -c syntax check")
		}
		t.Log("ruby not on PATH; skipping ruby -c syntax check")
		return
	}
	path := filepath.Join(t.TempDir(), "lazyssm.rb")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(ruby, "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("ruby -c failed: %v\n%s", err, b)
	}
}

func TestFormulaRejectsBadInput(t *testing.T) {
	tmpl := readFile(t, formulaTmplPath)
	cases := []struct{ name, tag, sha string }{
		{"tag without v", "0.3.0", hexC},
		{"tag missing patch", "v0.3", hexC},
		{"tag with prerelease", "v0.3.0-rc1", hexC},
		{"tag with trailing newline", "v0.3.0\n", hexC},
		{"empty tag", "", hexC},
		{"sha not hex", "v0.3.0", "xyz"},
		{"sha too short", "v0.3.0", strings.Repeat("c", 63)},
		{"sha too long", "v0.3.0", strings.Repeat("c", 65)},
		{"sha uppercase", "v0.3.0", strings.Repeat("C", 64)},
		{"sha with trailing newline", "v0.3.0", hexC + "\n"},
		{"empty sha", "v0.3.0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Formula(tmpl, tc.tag, tc.sha)
			if err == nil {
				t.Fatalf("expected error for tag %q sha %q, got output:\n%s", tc.tag, tc.sha, out)
			}
			if out != "" {
				t.Errorf("expected empty output on error, got:\n%s", out)
			}
		})
	}
}

func TestFormulaRejectsBrokenTemplate(t *testing.T) {
	for name, tmpl := range map[string]string{
		"parse error":   `sha256 "{{.SHA256"`,
		"unknown field": `sha256 "{{.Nope}}"`,
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := Formula(tmpl, "v0.3.0", hexC); err == nil || out != "" {
				t.Fatalf("expected error and empty output, got err=%v out=%q", err, out)
			}
		})
	}
}

func TestScoopManifestFromChecksums(t *testing.T) {
	out, err := ScoopManifest(readFile(t, scoopTmplPath), "v0.3.0", checksumsV030())
	if err != nil {
		t.Fatalf("ScoopManifest: %v", err)
	}
	m := decodeManifest(t, out)

	if m.Version != "0.3.0" {
		t.Errorf("version = %q, want 0.3.0", m.Version)
	}
	if m.Description != "Terminal UI for AWS SSM Session Manager" {
		t.Errorf("description = %q", m.Description)
	}
	if m.Homepage != "https://github.com/chalvinwz/lazyssm" {
		t.Errorf("homepage = %q", m.Homepage)
	}
	if m.License != "MIT" {
		t.Errorf("license = %q, want MIT", m.License)
	}
	amd := m.Architecture["64bit"]
	if want := "https://github.com/chalvinwz/lazyssm/releases/download/v0.3.0/lazyssm_0.3.0_windows_amd64.zip"; amd.URL != want {
		t.Errorf("64bit url = %q, want %q", amd.URL, want)
	}
	if amd.Hash != hexA {
		t.Errorf("64bit hash = %q, want %q", amd.Hash, hexA)
	}
	arm := m.Architecture["arm64"]
	if want := "https://github.com/chalvinwz/lazyssm/releases/download/v0.3.0/lazyssm_0.3.0_windows_arm64.zip"; arm.URL != want {
		t.Errorf("arm64 url = %q, want %q", arm.URL, want)
	}
	if arm.Hash != hexB {
		t.Errorf("arm64 hash = %q, want %q", arm.Hash, hexB)
	}
	if m.Bin != "lazyssm.exe" {
		t.Errorf("bin = %q, want lazyssm.exe", m.Bin)
	}
	if m.Checkver != "github" {
		t.Errorf("checkver = %q, want github", m.Checkver)
	}
	for _, arch := range []string{"64bit", "arm64"} {
		u := m.Autoupdate.Architecture[arch].URL
		if !strings.Contains(u, "$version") {
			t.Errorf("autoupdate %s url %q has no $version placeholder", arch, u)
		}
	}
	if got, want := m.Autoupdate.Architecture["64bit"].URL, "https://github.com/chalvinwz/lazyssm/releases/download/v$version/lazyssm_$version_windows_amd64.zip"; got != want {
		t.Errorf("autoupdate 64bit url = %q, want %q", got, want)
	}
	if got, want := m.Autoupdate.Architecture["arm64"].URL, "https://github.com/chalvinwz/lazyssm/releases/download/v$version/lazyssm_$version_windows_arm64.zip"; got != want {
		t.Errorf("autoupdate arm64 url = %q, want %q", got, want)
	}
}

func TestScoopManifestRequiresBothWindowsAssets(t *testing.T) {
	tmpl := readFile(t, scoopTmplPath)
	for name, drop := range map[string]string{
		"arm64 missing": "lazyssm_0.3.0_windows_arm64.zip",
		"amd64 missing": "lazyssm_0.3.0_windows_amd64.zip",
	} {
		t.Run(name, func(t *testing.T) {
			var kept []string
			for _, line := range strings.Split(strings.TrimSpace(checksumsV030()), "\n") {
				if !strings.HasSuffix(line, drop) {
					kept = append(kept, line)
				}
			}
			out, err := ScoopManifest(tmpl, "v0.3.0", strings.Join(kept, "\n")+"\n")
			if err == nil {
				t.Fatalf("expected error, got output:\n%s", out)
			}
			if out != "" {
				t.Errorf("expected empty output on error, got:\n%s", out)
			}
			if !strings.Contains(err.Error(), drop) {
				t.Errorf("error %q should name the missing asset %q", err, drop)
			}
		})
	}
}

func TestScoopManifestRejectsWrongVersionAssets(t *testing.T) {
	// Checksums for 0.2.0 must not satisfy a v0.3.0 render.
	out, err := ScoopManifest(readFile(t, scoopTmplPath), "v0.3.0", readFile(t, "testdata/checksums-v0.2.0.txt"))
	if err == nil {
		t.Fatalf("expected error, got output:\n%s", out)
	}
}

func TestScoopManifestRejectsBadInput(t *testing.T) {
	tmpl := readFile(t, scoopTmplPath)
	for name, tc := range map[string]struct{ tag, checksums string }{
		"tag without v":       {"0.3.0", checksumsV030()},
		"tag missing patch":   {"v0.3", checksumsV030()},
		"empty checksums":     {"v0.3.0", ""},
		"malformed checksums": {"v0.3.0", checksumsV030() + "not a checksum line\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := ScoopManifest(tmpl, tc.tag, tc.checksums); err == nil || out != "" {
				t.Fatalf("expected error and empty output, got err=%v out=%q", err, out)
			}
		})
	}
}

func TestScoopManifestRejectsInvalidJSONTemplate(t *testing.T) {
	out, err := ScoopManifest(`{"version": "{{.Version}}",}`, "v0.3.0", checksumsV030())
	if err == nil || out != "" {
		t.Fatalf("expected error and empty output for a template that renders invalid JSON, got err=%v out=%q", err, out)
	}
}

func TestParseChecksums(t *testing.T) {
	got, err := parseChecksums(checksumsV030())
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}
	if len(got) != 6 {
		t.Errorf("got %d entries, want 6: %v", len(got), got)
	}
	if got["lazyssm_0.3.0_windows_amd64.zip"] != hexA {
		t.Errorf("windows_amd64 hash = %q, want %q", got["lazyssm_0.3.0_windows_amd64.zip"], hexA)
	}
	if got["lazyssm_0.3.0_linux_arm64.tar.gz"] != hexD {
		t.Errorf("linux_arm64 hash = %q, want %q", got["lazyssm_0.3.0_linux_arm64.tar.gz"], hexD)
	}

	// Blank lines and CRLF line endings are tolerated.
	crlf := hexA + "  lazyssm_0.3.0_windows_amd64.zip\r\n\r\n" + hexB + "  lazyssm_0.3.0_windows_arm64.zip\r\n"
	got, err = parseChecksums(crlf)
	if err != nil || len(got) != 2 {
		t.Errorf("CRLF input: got %v, err %v; want 2 entries", got, err)
	}
}

func TestParseChecksumsRejectsMalformedLines(t *testing.T) {
	for name, in := range map[string]string{
		"63-char hash":       strings.Repeat("a", 63) + "  lazyssm_0.3.0_windows_amd64.zip\n",
		"65-char hash":       strings.Repeat("a", 65) + "  lazyssm_0.3.0_windows_amd64.zip\n",
		"uppercase hash":     strings.Repeat("A", 64) + "  lazyssm_0.3.0_windows_amd64.zip\n",
		"single space":       hexA + " lazyssm_0.3.0_windows_amd64.zip\n",
		"no filename":        hexA + "  \n",
		"hash only":          hexA + "\n",
		"garbage":            "garbage\n",
		"bad line after ok":  hexA + "  lazyssm_0.3.0_windows_amd64.zip\n" + strings.Repeat("a", 63) + "  other.zip\n",
		"duplicate filename": hexA + "  a.zip\n" + hexB + "  a.zip\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := parseChecksums(in); err == nil {
				t.Fatalf("expected error, got %v", got)
			}
		})
	}
}

func TestScoopManifestFromRealV020Checksums(t *testing.T) {
	checksums := readFile(t, "testdata/checksums-v0.2.0.txt")

	parsed, err := parseChecksums(checksums)
	if err != nil {
		t.Fatalf("parseChecksums(real v0.2.0): %v", err)
	}
	if len(parsed) != 6 {
		t.Errorf("real v0.2.0 checksums: got %d entries, want 6", len(parsed))
	}

	out, err := ScoopManifest(readFile(t, scoopTmplPath), "v0.2.0", checksums)
	if err != nil {
		t.Fatalf("ScoopManifest: %v", err)
	}
	m := decodeManifest(t, out)

	if m.Version != "0.2.0" {
		t.Errorf("version = %q, want 0.2.0", m.Version)
	}
	const (
		wantAMD64 = "c442e9172d23080aaefb170d16206788198151121d27687b23937ef4f1b100b5"
		wantARM64 = "d6c76a71ba13d422064df791a6692909be9f047d5d401973cbde0a94b579d285"
	)
	if got := m.Architecture["64bit"].Hash; got != wantAMD64 {
		t.Errorf("64bit hash = %q, want %q", got, wantAMD64)
	}
	if got := m.Architecture["arm64"].Hash; got != wantARM64 {
		t.Errorf("arm64 hash = %q, want %q", got, wantARM64)
	}
	if got, want := m.Architecture["64bit"].URL, "https://github.com/chalvinwz/lazyssm/releases/download/v0.2.0/lazyssm_0.2.0_windows_amd64.zip"; got != want {
		t.Errorf("64bit url = %q, want %q", got, want)
	}
	if got, want := m.Architecture["arm64"].URL, "https://github.com/chalvinwz/lazyssm/releases/download/v0.2.0/lazyssm_0.2.0_windows_arm64.zip"; got != want {
		t.Errorf("arm64 url = %q, want %q", got, want)
	}
}

// The release workflow invokes the CLI with exactly these flags; keep them
// stable.
func TestRunHomebrew(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"homebrew", "-tag", "v0.3.0", "-sha256", hexC, "-template", formulaTmplPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `sha256 "`+hexC+`"`) {
		t.Errorf("stdout missing rendered sha:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunScoop(t *testing.T) {
	checksums := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(checksums, []byte(checksumsV030()), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"scoop", "-tag", "v0.3.0", "-checksums", checksums, "-template", scoopTmplPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr.String())
	}
	if m := decodeManifest(t, stdout.String()); m.Architecture["64bit"].Hash != hexA {
		t.Errorf("64bit hash = %q, want %q", m.Architecture["64bit"].Hash, hexA)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunErrorsExitNonZero(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for name, args := range map[string][]string{
		"no subcommand":        {},
		"unknown subcommand":   {"nope"},
		"homebrew bad tag":     {"homebrew", "-tag", "0.3.0", "-sha256", hexC, "-template", formulaTmplPath},
		"homebrew bad sha":     {"homebrew", "-tag", "v0.3.0", "-sha256", "xyz", "-template", formulaTmplPath},
		"homebrew no tag":      {"homebrew", "-sha256", hexC, "-template", formulaTmplPath},
		"homebrew no sha":      {"homebrew", "-tag", "v0.3.0", "-template", formulaTmplPath},
		"homebrew no tmpl":     {"homebrew", "-tag", "v0.3.0", "-sha256", hexC},
		"homebrew tmpl gone":   {"homebrew", "-tag", "v0.3.0", "-sha256", hexC, "-template", missing},
		"homebrew bad flag":    {"homebrew", "-bogus"},
		"scoop no checksums":   {"scoop", "-tag", "v0.3.0", "-template", scoopTmplPath},
		"scoop checksums gone": {"scoop", "-tag", "v0.3.0", "-checksums", missing, "-template", scoopTmplPath},
		"scoop tmpl gone":      {"scoop", "-tag", "v0.3.0", "-checksums", "testdata/checksums-v0.2.0.txt", "-template", missing},
		"scoop wrong version":  {"scoop", "-tag", "v0.3.0", "-checksums", "testdata/checksums-v0.2.0.txt", "-template", scoopTmplPath},
		"scoop extra args":     {"scoop", "-tag", "v0.2.0", "-checksums", "testdata/checksums-v0.2.0.txt", "-template", scoopTmplPath, "stray"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code == 0 {
				t.Fatalf("expected non-zero exit; stdout:\n%s", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Error("expected a message on stderr")
			}
			if stdout.Len() != 0 {
				t.Errorf("expected empty stdout on error, got:\n%s", stdout.String())
			}
		})
	}
}
