package packaging

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Tests for publish.sh, the release workflow's helper that creates or
// updates one file in the tap or bucket repo through the GitHub Contents
// API. gh is always a fake that logs its argv: nothing here reaches GitHub.

// publishSH is run directly, not through a shell, so its shebang and exec
// bit are exercised too.
const publishSH = "./publish.sh"

const (
	pubRepo     = "chalvinwz/homebrew-tap"
	pubPath     = "Formula/lazyssm.rb"
	pubEndpoint = "repos/" + pubRepo + "/contents/" + pubPath
	pubMessage  = "lazyssm 1.2.3"
	// pubSHA is the blob sha the fake reports for the file already there.
	pubSHA = "3d2f0c5b7e9a8f6d4c1b0a9e8d7c6b5a4f3e2d1c"
)

// pubLocal is the file being published.
var pubLocal = []byte("class Lazyssm < Formula\n  desc \"Terminal UI for AWS SSM Session Manager\"\nend\n")

// pubTools are the real commands on publish.sh's hermetic PATH, besides the
// fake gh. cat is for the fake.
var pubTools = []string{"mktemp", "rm", "base64", "tr", "cmp", "sed", "grep", "cat"}

// fakeGHScript stands in for gh. It appends its argv to $FAKE_GH_LOG, one
// argument per line and an <end> line per call, then answers as gh does.
// A GET prints what publish.sh's --jq filter would (sha, encoding, then
// the base64 content) from $FAKE_GH_GET_OUT, or fails with a 404 or a 500
// as $FAKE_GH_GET says. A PUT prints nothing (publish.sh passes --silent)
// and succeeds unless $FAKE_GH_PUT is "error".
const fakeGHScript = `#!/bin/sh
for arg in "$@"; do
	printf '%s\n' "$arg"
done >>"$FAKE_GH_LOG"
printf '<end>\n' >>"$FAKE_GH_LOG"
if [ -z "${GH_TOKEN:-}" ]; then
	echo 'gh: set the GH_TOKEN environment variable' >&2
	exit 4
fi
method=GET
prev=
for arg in "$@"; do
	case $prev in
	-X | --method) method=$arg ;;
	esac
	prev=$arg
done
case $method in
GET)
	case ${FAKE_GH_GET:-} in
	absent)
		echo '{"message":"Not Found","documentation_url":"https://docs.github.com/rest/repos/contents#get-repository-content","status":"404"}'
		echo 'gh: Not Found (HTTP 404)' >&2
		exit 1
		;;
	present)
		cat "$FAKE_GH_GET_OUT"
		;;
	*)
		echo '{"message":"Server Error","status":"500"}'
		echo 'gh: Server Error (HTTP 500)' >&2
		exit 1
		;;
	esac
	;;
PUT)
	if [ "${FAKE_GH_PUT:-}" = error ]; then
		echo '{"message":"Conflict","status":"409"}'
		echo 'gh: Conflict (HTTP 409)' >&2
		exit 1
	fi
	;;
*)
	echo "fake gh: unexpected method $method" >&2
	exit 99
	;;
esac
`

// ghFake is one publish.sh run's world: a fake gh, the real tools, the local
// file, and the env publish.sh runs with.
type ghFake struct {
	root  string
	log   string // the fake gh's argv log
	local string // the file being published
	env   map[string]string
}

// newGHFake sets up a run whose GET answers as get says: "absent" (404),
// "present" (call current to set what is there), or "error" (500).
func newGHFake(t *testing.T, get string) *ghFake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("publish.sh runs only in the Linux release job")
	}
	root := t.TempDir()
	fakes := mkdir(t, root, "fakebin")
	if err := os.WriteFile(filepath.Join(fakes, "gh"), []byte(fakeGHScript), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	f := &ghFake{
		root:  root,
		log:   filepath.Join(root, "gh.log"),
		local: filepath.Join(root, "lazyssm.rb"),
	}
	if err := os.WriteFile(f.local, pubLocal, 0o644); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	f.env = map[string]string{
		"PATH":            fakes + ":" + toolPath(t, pubTools...),
		"HOME":            mkdir(t, root, "home"),
		"TMPDIR":          mkdir(t, root, "tmp"),
		"GH_TOKEN":        "fake-token",
		"FAKE_GH_LOG":     f.log,
		"FAKE_GH_GET":     get,
		"FAKE_GH_GET_OUT": filepath.Join(root, "get.out"),
	}
	return f
}

// current sets the file the fake reports as already in the repo, encoded
// as the Contents API encodes it: base64 wrapped at 60 columns.
func (f *ghFake) current(t *testing.T, body []byte) {
	t.Helper()
	f.getOutput(t, pubSHA+"\nbase64\n"+githubBase64(body))
}

// getOutput sets the fake GET's raw output.
func (f *ghFake) getOutput(t *testing.T, out string) {
	t.Helper()
	if err := os.WriteFile(f.env["FAKE_GH_GET_OUT"], []byte(out), 0o644); err != nil {
		t.Fatalf("write fake GET output: %v", err)
	}
}

// run runs publish.sh with args, or with the default publish of the local
// file when none are given.
func (f *ghFake) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if len(args) == 0 {
		args = []string{pubRepo, pubPath, f.local, pubMessage}
	}
	return runScript(t, f.env, nil, publishSH, args...)
}

// calls returns the fake gh's logged invocations, in order.
func (f *ghFake) calls(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	var calls [][]string
	var cur []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line == "<end>" {
			calls = append(calls, cur)
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	if cur != nil {
		t.Fatalf("gh log ends mid-call: %q", cur)
	}
	return calls
}

// githubBase64 encodes b as the Contents API does: wrapped at 60 columns,
// each line ending in a newline.
func githubBase64(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var sb strings.Builder
	for len(enc) > 60 {
		sb.WriteString(enc[:60] + "\n")
		enc = enc[60:]
	}
	sb.WriteString(enc + "\n")
	return sb.String()
}

// ghMethod returns the HTTP method of a logged `gh api` call.
func ghMethod(call []string) string {
	for i := 0; i+1 < len(call); i++ {
		if call[i] == "-X" || call[i] == "--method" {
			return call[i+1]
		}
	}
	return "GET"
}

// ghFields returns the -f/--raw-field key=value pairs of a logged call.
func ghFields(t *testing.T, call []string) map[string]string {
	t.Helper()
	fields := map[string]string{}
	for i := 0; i+1 < len(call); i++ {
		if call[i] != "-f" && call[i] != "--raw-field" {
			continue
		}
		k, v, ok := strings.Cut(call[i+1], "=")
		if !ok {
			t.Fatalf("field %q is not key=value", call[i+1])
		}
		if _, dup := fields[k]; dup {
			t.Fatalf("field %q given twice in %q", k, call)
		}
		fields[k] = v
	}
	if slices.Contains(call, "-F") || slices.Contains(call, "--field") {
		t.Errorf("call uses -F, which converts values; want -f: %q", call)
	}
	return fields
}

// assertGET checks call is a plain GET of the published file.
func assertGET(t *testing.T, call []string) {
	t.Helper()
	if len(call) == 0 || call[0] != "api" {
		t.Fatalf("call is not `gh api`: %q", call)
	}
	if m := ghMethod(call); m != "GET" {
		t.Errorf("read call method = %s, want GET: %q", m, call)
	}
	if !slices.Contains(call, pubEndpoint) {
		t.Errorf("read call does not name %s: %q", pubEndpoint, call)
	}
	if len(ghFields(t, call)) != 0 {
		t.Errorf("read call sends fields: %q", call)
	}
	// The fake gh ignores --jq, so check the filter here: publish.sh reads
	// its output as these three lines.
	const filter = ".sha, .encoding, .content"
	if i := slices.Index(call, "--jq"); i < 0 || i+1 >= len(call) || call[i+1] != filter {
		t.Errorf("read call does not pass --jq %q: %q", filter, call)
	}
}

// assertPUT checks call writes the local file with the commit message and
// returns its fields.
func assertPUT(t *testing.T, call []string) map[string]string {
	t.Helper()
	if len(call) == 0 || call[0] != "api" {
		t.Fatalf("call is not `gh api`: %q", call)
	}
	if m := ghMethod(call); m != "PUT" {
		t.Errorf("write call method = %s, want PUT: %q", m, call)
	}
	if !slices.Contains(call, pubEndpoint) {
		t.Errorf("write call does not name %s: %q", pubEndpoint, call)
	}
	fields := ghFields(t, call)
	if fields["message"] != pubMessage {
		t.Errorf("message = %q, want %q", fields["message"], pubMessage)
	}
	content, ok := fields["content"]
	if !ok {
		t.Fatalf("write call has no content field: %q", call)
	}
	if strings.ContainsAny(content, "\n\r ") {
		t.Errorf("content has whitespace in it: %q", content)
	}
	got, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("content is not base64: %v", err)
	}
	if string(got) != string(pubLocal) {
		t.Errorf("content decodes to %q, want the local file %q", got, pubLocal)
	}
	return fields
}

func TestPublishCreatesAbsentFile(t *testing.T) {
	f := newGHFake(t, "absent")
	stdout, stderr, code := f.run(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := f.calls(t)
	if len(calls) != 2 {
		t.Fatalf("gh called %d times, want 2 (read, then create): %q", len(calls), calls)
	}
	assertGET(t, calls[0])
	fields := assertPUT(t, calls[1])
	if sha, ok := fields["sha"]; ok {
		t.Errorf("create sends sha %q; a new file has none", sha)
	}
	if !strings.Contains(stdout, "created") {
		t.Errorf("stdout does not say the file was created:\n%s", stdout)
	}
}

func TestPublishUpdatesChangedFile(t *testing.T) {
	f := newGHFake(t, "present")
	f.current(t, []byte("class Lazyssm < Formula\n  # the previous release\nend\n"))
	stdout, stderr, code := f.run(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := f.calls(t)
	if len(calls) != 2 {
		t.Fatalf("gh called %d times, want 2 (read, then update): %q", len(calls), calls)
	}
	assertGET(t, calls[0])
	fields := assertPUT(t, calls[1])
	if fields["sha"] != pubSHA {
		t.Errorf("update sends sha %q, want the current file's %q", fields["sha"], pubSHA)
	}
	if !strings.Contains(stdout, "updated") {
		t.Errorf("stdout does not say the file was updated:\n%s", stdout)
	}
}

func TestPublishSkipsUnchangedFile(t *testing.T) {
	f := newGHFake(t, "present")
	f.current(t, pubLocal)
	stdout, stderr, code := f.run(t)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	calls := f.calls(t)
	if len(calls) != 1 {
		t.Fatalf("gh called %d times, want 1 (read only, no write): %q", len(calls), calls)
	}
	assertGET(t, calls[0])
	if !strings.Contains(stdout, "unchanged") {
		t.Errorf("stdout does not say unchanged:\n%s", stdout)
	}
}

func TestPublishFailsOnAPIError(t *testing.T) {
	cases := []struct {
		name      string
		get       string
		current   []byte // what the repo holds, for get "present"
		rawGet    string // raw GET output instead of current
		putError  bool
		wantCalls int
		wantErr   string
	}{
		{name: "read fails", get: "error", wantCalls: 1, wantErr: "HTTP 500"},
		{name: "create fails", get: "absent", putError: true, wantCalls: 2, wantErr: "HTTP 409"},
		{name: "update fails", get: "present", current: []byte("old\n"), putError: true, wantCalls: 2, wantErr: "HTTP 409"},
		{name: "read has no sha", get: "present", rawGet: "\nbase64\n" + githubBase64(pubLocal), wantCalls: 1},
		{name: "read is not base64", get: "present", rawGet: pubSHA + "\nnone\n\n", wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGHFake(t, tc.get)
			switch {
			case tc.rawGet != "":
				f.getOutput(t, tc.rawGet)
			case tc.current != nil:
				f.current(t, tc.current)
			}
			if tc.putError {
				f.env["FAKE_GH_PUT"] = "error"
			}
			stdout, stderr, code := f.run(t)
			if code == 0 {
				t.Fatalf("exit 0, want non-zero\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if calls := f.calls(t); len(calls) != tc.wantCalls {
				t.Errorf("gh called %d times, want %d: %q", len(calls), tc.wantCalls, calls)
			}
			if tc.wantErr != "" && !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr does not pass on gh's %q:\n%s", tc.wantErr, stderr)
			}
			if strings.Contains(stdout, "unchanged") || strings.Contains(stdout, "created") || strings.Contains(stdout, "updated") {
				t.Errorf("stdout reports success:\n%s", stdout)
			}
		})
	}
}

func TestPublishRejectsBadInvocation(t *testing.T) {
	cases := []struct {
		name  string
		unset string // env var to remove
		args  func(f *ghFake) []string
	}{
		{name: "no GH_TOKEN", unset: "GH_TOKEN"},
		{name: "too few args", args: func(f *ghFake) []string { return []string{pubRepo, pubPath, f.local} }},
		{name: "too many args", args: func(f *ghFake) []string { return []string{pubRepo, pubPath, f.local, pubMessage, "extra"} }},
		{name: "local file missing", args: func(f *ghFake) []string {
			return []string{pubRepo, pubPath, filepath.Join(f.root, "missing.rb"), pubMessage}
		}},
		{name: "empty message", args: func(f *ghFake) []string { return []string{pubRepo, pubPath, f.local, ""} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGHFake(t, "absent")
			if tc.unset != "" {
				delete(f.env, tc.unset)
			}
			var args []string
			if tc.args != nil {
				args = tc.args(f)
			}
			stdout, stderr, code := f.run(t, args...)
			if code == 0 {
				t.Fatalf("exit 0, want non-zero\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if stderr == "" {
				t.Error("no error message on stderr")
			}
			if calls := f.calls(t); len(calls) != 0 {
				t.Errorf("gh called before the invocation was checked: %q", calls)
			}
		})
	}
}
