package packaging

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const workflowGlob = "../.github/workflows/*.yml"

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

// TestWorkflowActionsPinnedBySHA enforces the repo rule that every GitHub
// Action is pinned to a full commit SHA, never a movable tag or branch.
func TestWorkflowActionsPinnedBySHA(t *testing.T) {
	files, err := filepath.Glob(workflowGlob)
	if err != nil {
		t.Fatalf("glob %s: %v", workflowGlob, err)
	}
	if len(files) == 0 {
		t.Fatalf("no workflow files match %s", workflowGlob)
	}
	for _, f := range files {
		for i, line := range readLines(t, f) {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "uses:") && !strings.HasPrefix(trimmed, "- uses:") {
				continue
			}
			if !pinnedUses.MatchString(line) {
				t.Errorf("%s:%d: action not pinned as owner/repo@<40-hex sha> # vX.Y.Z: %q", f, i+1, trimmed)
			}
		}
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
