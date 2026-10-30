package packaging

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Shared fixtures for the installer tests. Nothing here touches the network:
// a fake release is a local directory that LAZYSSM_DOWNLOAD_BASE points at.
// This file builds on every OS; only the tests that run sh skip Windows.

// installSH is the script under test, relative to this package's directory.
const installSH = "../install.sh"

// scriptTimeout bounds one script run, so a hang such as a read from stdin
// fails the test instead of stalling the suite.
const scriptTimeout = 2 * time.Minute

// fixtureModTime keeps fixture archives reproducible.
var fixtureModTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fileSpec is one regular file inside a fixture archive.
type fileSpec struct {
	Body []byte
	Mode os.FileMode
}

// fakeBinarySH returns a stand-in lazyssm: a script that prints what
// `lazyssm --version` prints, so the tests never need a real build.
func fakeBinarySH(version string) []byte {
	return []byte("#!/bin/sh\necho \"lazyssm " + version + "\"\n")
}

// tarGz builds a .tar.gz holding files at the archive root, as GoReleaser
// lays out the macOS and Linux release assets.
func tarGz(t *testing.T, files map[string]fileSpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f := files[name]
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     int64(f.Mode.Perm()),
			Size:     int64(len(f.Body)),
			ModTime:  fixtureModTime,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(f.Body); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// zipFile builds a .zip holding files at the archive root, as GoReleaser lays
// out the Windows release assets.
func zipFile(t *testing.T, files map[string]fileSpec) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f := files[name]
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: fixtureModTime}
		hdr.SetMode(f.Mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip header %s: %v", name, err)
		}
		if _, err := w.Write(f.Body); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// fakeRelease writes a release the way GitHub serves it, as
// <base>/<tag>/<name> for each asset plus a correct checksums.txt in
// GoReleaser's format, and returns base for LAZYSSM_DOWNLOAD_BASE.
func fakeRelease(t *testing.T, tag string, assets map[string][]byte) (base string) {
	t.Helper()
	base = t.TempDir()
	dir := filepath.Join(base, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	var sums strings.Builder
	for _, name := range slices.Sorted(maps.Keys(assets)) {
		body := assets[name]
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatalf("write asset %s: %v", name, err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(body), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644); err != nil {
		t.Fatalf("write checksums.txt: %v", err)
	}
	return base
}

// toolPath returns a temp dir of symlinks to the real tools, for a hermetic
// PATH that holds only what a test means the script to find. A tool this
// machine lacks is left out, so alternatives such as sha256sum and shasum can
// both be listed.
func toolPath(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		found, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if err := os.Symlink(found, filepath.Join(dir, tool)); err != nil {
			t.Fatalf("link %s: %v", tool, err)
		}
	}
	return dir
}

// runInstallSH runs `/bin/sh ../install.sh` with exactly env: nothing is
// inherited from the test process.
func runInstallSH(t *testing.T, env map[string]string) (stdout, stderr string, code int) {
	t.Helper()
	return runScript(t, env, nil, "/bin/sh", installSH)
}

// runScript runs name with args and exactly env, with stdin as its standard
// input (nil means none), and returns its output and exit code. It fails the
// test if the command cannot start or outlives scriptTimeout.
func runScript(t *testing.T, env map[string]string, stdin io.Reader, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runScriptIn(t, "", env, stdin, name, args...)
}

// runScriptIn is runScript with dir as the working directory ("" means this
// package's directory).
func runScriptIn(t *testing.T, dir string, env map[string]string, stdin io.Reader, name string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = make([]string, 0, len(env)) // non-nil, so nothing is inherited
	for _, k := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, k+"="+env[k])
	}
	cmd.Stdin = stdin
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		code = exitErr.ExitCode()
	default:
		t.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", name, strings.Join(args, " "), err, out.String(), errOut.String())
	}
	return out.String(), errOut.String(), code
}
