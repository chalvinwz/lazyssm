// Command lazyssm is a terminal UI for AWS SSM Session Manager.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/preflight"
	"github.com/chalvinwz/lazyssm/internal/store"
	"github.com/chalvinwz/lazyssm/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

// loadTimeout bounds AWS config/credential resolution so a wedged IMDS or
// credential provider can't hang startup indefinitely.
const loadTimeout = 15 * time.Second

func main() {
	profile := flag.String("profile", "", "AWS profile to use (overrides AWS_PROFILE)")
	region := flag.String("region", "", "AWS region to use (overrides AWS_REGION)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("lazyssm", version)
		return
	}

	switch flag.Arg(0) {
	case "doctor":
		os.Exit(runDoctor(*profile, *region))
	case "", "tui":
		os.Exit(runTUI(*profile, *region))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", flag.Arg(0))
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `lazyssm — a terminal UI for AWS SSM Session Manager

Usage:
  lazyssm [--profile P] [--region R]          launch the TUI
  lazyssm [--profile P] [--region R] doctor   run environment checks

Flags:
  --profile  AWS profile (overrides AWS_PROFILE)
  --region   AWS region (overrides AWS_REGION)
`)
}

func runTUI(profile, region string) int {
	// Demo mode (LAZYSSM_DEMO=1) serves fixed sample data without touching AWS,
	// for recording screenshots/GIFs.
	if os.Getenv("LAZYSSM_DEMO") != "" {
		m := ui.NewDemo(profile, region)
		if _, err := tea.NewProgram(m).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	clients, err := awscfg.Load(ctx, profile, region)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to load AWS config:", err)
		return 1
	}
	st, err := store.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not load local store:", err)
		st, _ = store.LoadFrom("") // in-memory fallback
	}
	m := ui.New(clients, st, profile, region)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func runDoctor(profile, region string) int {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	clients, err := awscfg.Load(ctx, profile, region)
	var checks []preflight.Check
	if err != nil {
		// Still report binary checks; credential checks can't run without config.
		checks = preflight.CheckBinaries()
		checks = append(checks, preflight.Check{
			Name:   "AWS config",
			Detail: err.Error(),
			Fix:    "run `aws configure` or set AWS_PROFILE/AWS_REGION",
		})
	} else {
		checks = preflight.Run(ctx, preflight.Params{
			Profile: profile,
			Region:  clients.Region(),
			STS:     clients.STS,
		})
	}

	fmt.Println("lazyssm doctor")
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		fmt.Printf("  %s %s — %s\n", mark, c.Name, c.Detail)
		if !c.OK && c.Fix != "" {
			fmt.Printf("      fix: %s\n", c.Fix)
		}
	}
	if preflight.AllOK(checks) {
		fmt.Println("\nall checks passed")
		return 0
	}
	fmt.Println("\nsome checks failed")
	return 1
}
