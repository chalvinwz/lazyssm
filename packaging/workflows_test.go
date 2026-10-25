package packaging

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const workflowDir = "../.github/workflows"

// workflowPatterns match every workflow file GitHub runs: it reads both
// extensions.
var workflowPatterns = []string{"*.yml", "*.yaml"}

// usesLine picks out the lines the pin check applies to: a `uses:` key,
// also as a list item, whatever the spacing (`-  uses:` too).
var usesLine = regexp.MustCompile(`^\s*-?\s*uses:`)

// pinnedUses is the only accepted shape of a `uses:` line: owner/repo@<full
// 40-hex commit SHA> followed by the human-readable version as a comment.
var pinnedUses = regexp.MustCompile(`^\s*-?\s*uses:\s*[\w.-]+/[\w./-]+@[0-9a-f]{40}\s+#\s+v\d+\.\d+\.\d+\s*$`)

// exactGoReleaser is a fully specified GoReleaser release tag. Quotes are
// stripped before matching.
var exactGoReleaser = regexp.MustCompile(`^v2\.\d+\.\d+$`)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(string(b), "\n")
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// workflowFiles returns the workflow files in dir, of either extension.
func workflowFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	for _, pattern := range workflowPatterns {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		files = append(files, matches...)
	}
	return files
}

// unpinnedActions returns "file:line: text" for each `uses:` line in files
// that is not pinned as owner/repo@<40-hex sha> # vX.Y.Z.
func unpinnedActions(t *testing.T, files []string) []string {
	t.Helper()
	var bad []string
	for _, f := range files {
		for i, line := range readLines(t, f) {
			if usesLine.MatchString(line) && !pinnedUses.MatchString(line) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s", f, i+1, strings.TrimSpace(line)))
			}
		}
	}
	return bad
}

// TestWorkflowActionsPinnedBySHA enforces the repo rule that every GitHub
// Action is pinned to a full commit SHA, never a movable tag or branch.
func TestWorkflowActionsPinnedBySHA(t *testing.T) {
	files := workflowFiles(t, workflowDir)
	if len(files) == 0 {
		t.Fatalf("no workflow files in %s", workflowDir)
	}
	for _, bad := range unpinnedActions(t, files) {
		t.Errorf("action not pinned as owner/repo@<40-hex sha> # vX.Y.Z: %s", bad)
	}
}

// TestWorkflowPinCheckSeesEveryUsesLine checks the pin check itself: it
// must read .yaml files as well as .yml, and a `uses:` line however it is
// spaced.
func TestWorkflowPinCheckSeesEveryUsesLine(t *testing.T) {
	dir := t.TempDir()
	pinned := "      - uses: actions/checkout@" + strings.Repeat("0", 40) + " # v7.0.1\n"
	for name, body := range map[string]string{
		"a.yml":  "steps:\n" + pinned + "      -  uses: actions/checkout@v7\n",
		"b.yaml": "steps:\n" + pinned + "      - uses: actions/setup-go@v7\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got := unpinnedActions(t, workflowFiles(t, dir))
	want := []string{
		filepath.Join(dir, "a.yml") + ":3: -  uses: actions/checkout@v7",
		filepath.Join(dir, "b.yaml") + ":3: - uses: actions/setup-go@v7",
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("unpinned actions found:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestGoReleaserVersionExact enforces an exact GoReleaser release in the
// release workflow instead of a floating range such as "~> v2".
func TestGoReleaserVersionExact(t *testing.T) {
	const path = "../.github/workflows/release.yml"
	lines := readLines(t, path)

	for i, line := range lines {
		if strings.Contains(line, "~>") {
			t.Errorf("%s:%d: floating version constraint %q", path, i+1, strings.TrimSpace(line))
		}
	}

	step := -1
	for i, line := range lines {
		if strings.Contains(line, "uses: goreleaser/goreleaser-action@") {
			step = i
			break
		}
	}
	if step < 0 {
		t.Fatalf("%s: goreleaser-action step not found", path)
	}

	// The step ends at the next list item at (or left of) the `uses:` line's
	// indent, or at anything dedented past it.
	stepIndent := indentOf(lines[step])
	found := false
	for i := step + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		ind := indentOf(line)
		if ind < stepIndent || (ind <= stepIndent && strings.HasPrefix(trimmed, "- ")) {
			break
		}
		if !strings.HasPrefix(trimmed, "version:") {
			continue
		}
		found = true
		val := strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
		if j := strings.Index(val, " #"); j >= 0 {
			val = strings.TrimSpace(val[:j])
		}
		val = strings.Trim(val, `"'`)
		if !exactGoReleaser.MatchString(val) {
			t.Errorf("%s:%d: goreleaser version %q is not an exact v2.x.y release", path, i+1, val)
		}
	}
	if !found {
		t.Errorf("%s: goreleaser step has no `version:` input", path)
	}
}
