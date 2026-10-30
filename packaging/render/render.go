package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// releaseDownloadBase is where GoReleaser publishes the release assets.
const releaseDownloadBase = "https://github.com/chalvinwz/lazyssm/releases/download"

var (
	tagRE    = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// checksumLineRE matches one line of GoReleaser's checksums.txt.
	checksumLineRE = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)
)

type formulaData struct {
	Tag     string
	Version string
	SHA256  string
}

type scoopData struct {
	Tag       string
	Version   string
	AMD64URL  string
	AMD64Hash string
	ARM64URL  string
	ARM64Hash string
}

// Formula renders the Homebrew formula template for tag (vX.Y.Z) and the
// sha256 (64 lowercase hex digits) of that tag's source tarball.
func Formula(tmpl, tag, sha256 string) (string, error) {
	version, err := versionOf(tag)
	if err != nil {
		return "", err
	}
	if !sha256RE.MatchString(sha256) {
		return "", fmt.Errorf("invalid sha256 %q: want 64 lowercase hex digits", sha256)
	}
	return execute("formula", tmpl, formulaData{Tag: tag, Version: version, SHA256: sha256})
}

// ScoopManifest renders the Scoop manifest template for tag (vX.Y.Z) using the
// release's checksums.txt contents, which must list both Windows zip assets.
// It fails if the rendered output is not valid JSON.
func ScoopManifest(tmpl, tag, checksums string) (string, error) {
	version, err := versionOf(tag)
	if err != nil {
		return "", err
	}
	sums, err := parseChecksums(checksums)
	if err != nil {
		return "", err
	}
	amd64File := fmt.Sprintf("lazyssm_%s_windows_amd64.zip", version)
	arm64File := fmt.Sprintf("lazyssm_%s_windows_arm64.zip", version)
	amd64Hash, ok := sums[amd64File]
	if !ok {
		return "", fmt.Errorf("checksums has no entry for %s", amd64File)
	}
	arm64Hash, ok := sums[arm64File]
	if !ok {
		return "", fmt.Errorf("checksums has no entry for %s", arm64File)
	}

	out, err := execute("scoop", tmpl, scoopData{
		Tag:       tag,
		Version:   version,
		AMD64URL:  fmt.Sprintf("%s/%s/%s", releaseDownloadBase, tag, amd64File),
		AMD64Hash: amd64Hash,
		ARM64URL:  fmt.Sprintf("%s/%s/%s", releaseDownloadBase, tag, arm64File),
		ARM64Hash: arm64Hash,
	})
	if err != nil {
		return "", err
	}
	if !json.Valid([]byte(out)) {
		return "", fmt.Errorf("rendered scoop manifest is not valid JSON")
	}
	return out, nil
}

// parseChecksums parses a checksums.txt (`<sha256>  <file>` per line) into a
// file name to hash map. Blank lines are ignored; any other malformed line, or
// a file listed twice, is an error.
func parseChecksums(s string) (map[string]string, error) {
	sums := make(map[string]string)
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := checksumLineRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("checksums line %d is malformed: want \"<64 lowercase hex>  <file>\", got %q", i+1, line)
		}
		hash, file := m[1], m[2]
		if _, dup := sums[file]; dup {
			return nil, fmt.Errorf("checksums line %d: duplicate entry for %s", i+1, file)
		}
		sums[file] = hash
	}
	return sums, nil
}

// versionOf validates tag and returns it without the leading "v".
func versionOf(tag string) (string, error) {
	if !tagRE.MatchString(tag) {
		return "", fmt.Errorf("invalid tag %q: want vX.Y.Z", tag)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

func execute(name, tmpl string, data any) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse %s template: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s template: %w", name, err)
	}
	return buf.String(), nil
}
