// Command render fills the Homebrew formula and Scoop manifest templates for a
// release. The release workflow runs it as:
//
//	go run ./packaging/render homebrew -tag vX.Y.Z -sha256 <hex> -template packaging/homebrew/lazyssm.rb.tmpl
//	go run ./packaging/render scoop -tag vX.Y.Z -checksums <file> -template packaging/scoop/lazyssm.json.tmpl
//
// The rendered file goes to stdout; any error goes to stderr with a non-zero
// exit status.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage:
  render homebrew -tag vX.Y.Z -sha256 <hex> -template <file>
  render scoop    -tag vX.Y.Z -checksums <file> -template <file>
`

// run executes the CLI and returns its exit code. It writes to stdout only
// when rendering fully succeeds.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		report(stderr, "render: missing subcommand\n"+usage)
		return 2
	}
	if args[0] != "homebrew" && args[0] != "scoop" {
		report(stderr, "render: unknown subcommand %q\n%s", args[0], usage)
		return 2
	}
	out, err := dispatch(args[0], args[1:], stderr)
	if err != nil {
		report(stderr, "render %s: %v\n", args[0], err)
		return 1
	}
	if _, err := io.WriteString(stdout, out); err != nil {
		report(stderr, "render %s: write output: %v\n", args[0], err)
		return 1
	}
	return 0
}

// report writes a diagnostic to w. Its write error is dropped because there
// is nowhere left to report it.
func report(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func dispatch(cmd string, args []string, stderr io.Writer) (string, error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	tag := fs.String("tag", "", "release tag, vX.Y.Z (required)")
	tmplPath := fs.String("template", "", "path to the template file (required)")

	// Register only the chosen subcommand's flags so the other's are rejected.
	var sha256, checksumsPath *string
	if cmd == "homebrew" {
		sha256 = fs.String("sha256", "", "sha256 of the tag's source tarball (required)")
	} else {
		checksumsPath = fs.String("checksums", "", "path to the release's checksums.txt (required)")
	}

	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *tag == "" {
		return "", fmt.Errorf("-tag is required")
	}
	if *tmplPath == "" {
		return "", fmt.Errorf("-template is required")
	}

	if cmd == "homebrew" {
		if *sha256 == "" {
			return "", fmt.Errorf("-sha256 is required")
		}
		tmpl, err := os.ReadFile(*tmplPath)
		if err != nil {
			return "", err
		}
		return Formula(string(tmpl), *tag, *sha256)
	}

	if *checksumsPath == "" {
		return "", fmt.Errorf("-checksums is required")
	}
	tmpl, err := os.ReadFile(*tmplPath)
	if err != nil {
		return "", err
	}
	checksums, err := os.ReadFile(*checksumsPath)
	if err != nil {
		return "", err
	}
	return ScoopManifest(string(tmpl), *tag, string(checksums))
}
