// Command lazyssm is a terminal UI for AWS SSM Session Manager.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/chalvinwz/lazyssm/internal/awscfg"
	"github.com/chalvinwz/lazyssm/internal/preflight"
	"github.com/chalvinwz/lazyssm/internal/store"
	"github.com/chalvinwz/lazyssm/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

// loadTimeout bounds AWS config and credential resolution so a wedged IMDS or
// credential provider cannot hang startup.
const loadTimeout = 15 * time.Second

func main() {
	profile := flag.String("profile", "", "AWS profile to use (overrides AWS_PROFILE)")
	region := flag.String("region", "", "AWS region to use (overrides AWS_REGION)")
	autoLogin := flag.Bool("auto-login", false, "run 'aws sso login' automatically when the SSO session is expired")
	theme := flag.String("theme", "", "theme preset (overrides the local configuration)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("lazyssm", version)
		return
	}

	if *theme != "" && !ui.ValidTheme(*theme) {
		fmt.Fprintf(os.Stderr, "unknown theme %q\n", *theme)
		os.Exit(2)
	}
	switch flag.Arg(0) {
	case "doctor":
		os.Exit(runDoctor(*profile, *region))
	case "", "tui":
		os.Exit(runTUI(*profile, *region, *autoLogin, *theme))
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
  --profile     AWS profile (overrides AWS_PROFILE)
  --region      AWS region (overrides AWS_REGION)
  --theme       default, tokyo-night, dracula, nord, catppuccin-mocha
  --auto-login  run 'aws sso login' automatically when the SSO session is expired
`)
}

func runTUI(flagProfile, flagRegion string, autoLogin bool, theme string) int {
	// Demo mode (LAZYSSM_DEMO=1) serves fixed sample data without touching AWS,
	// for recording screenshots/GIFs.
	if os.Getenv("LAZYSSM_DEMO") != "" {
		m := ui.NewDemo(flagProfile, flagRegion)
		if theme != "" {
			m = m.WithTheme(theme)
		}
		if _, err := tea.NewProgram(m).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	}

	st, err := store.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not load local store:", err)
		st, _ = store.LoadFrom("") // in-memory fallback
	}
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	profile, region, fromState := startProfile(flagProfile, flagRegion, st)
	clients, err := awscfg.Load(ctx, profile, region)
	if err != nil && fromState {
		// The saved profile may since have been removed from the AWS config.
		why := err.Error()
		if _, ok := awscfg.MissingProfile(err); ok {
			why = "it's no longer in your AWS config"
		}
		fmt.Fprintf(os.Stderr, "warning: last used profile %q didn't load (%s); using the default\n", profile, why)
		profile, region = "", flagRegion
		clients, err = awscfg.Load(ctx, profile, region)
	}
	if err != nil {
		if help, ok := profileHelp(err); ok {
			fmt.Fprintln(os.Stderr, "lazyssm: "+strings.Join(help, "\n  "))
		} else {
			fmt.Fprintln(os.Stderr, "lazyssm: can't load the AWS config:", err)
		}
		return 1
	}
	// The SDK accepts any region string and fails only once a request cannot
	// reach its endpoint, so stop on one that cannot be a region code at all.
	if help := regionHelp(clients.Region(), regionSource(flagRegion, region)); help != nil {
		fmt.Fprintln(os.Stderr, "lazyssm: "+strings.Join(help, "\n  "))
		return 1
	}
	m := ui.New(clients, st, profile, region, autoLogin || st.AutoLogin)
	if theme != "" {
		m = m.WithTheme(theme)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// profileHelp explains a profile missing from the AWS config: the files read,
// the closest names, and how to list them all. ok is false for other errors.
func profileHelp(err error) (lines []string, ok bool) {
	name, ok := awscfg.MissingProfile(err)
	if !ok {
		return nil, false
	}
	configPath, credentialsPath := awscfg.SharedFiles()
	lines = []string{fmt.Sprintf("profile %q isn't in %s or %s", name, homeRelative(configPath), homeRelative(credentialsPath))}
	profiles, _ := awscfg.Profiles()
	if len(profiles) == 0 {
		return append(lines, "no profiles are set up yet: add one with aws configure sso"), true
	}
	if near := awscfg.Suggest(name, profiles); len(near) > 0 {
		lines = append(lines, "did you mean "+strings.Join(near, ", ")+"?")
	}
	return append(lines, "see them all with: aws configure list-profiles"), true
}

// regionHelp explains a value that cannot be a region code: where it came from,
// the likeliest intended regions, and how to pick another. It returns nil for
// an empty or well-formed region, as an unlisted code may be a newer region.
func regionHelp(region, source string) []string {
	if region == "" || awscfg.LooksLikeRegion(region) {
		return nil
	}
	lines := []string{fmt.Sprintf("region %q (from %s) isn't an AWS region code", region, source)}
	if near := preflight.RegionHint(region); near != "" {
		lines = append(lines, "did you mean "+near+"?")
	}
	switch source {
	case "--region":
		return append(lines, "or run lazyssm without --region and press R to pick one")
	case "AWS_REGION", "AWS_DEFAULT_REGION":
		return append(lines, "or unset "+source+" and press R in lazyssm to pick one")
	}
	return append(lines, "or fix region in the profile, or pass --region")
}

// regionSource names where the starting region came from, for messages.
func regionSource(flagRegion, startRegion string) string {
	switch {
	case flagRegion != "":
		return "--region"
	case os.Getenv("AWS_REGION") != "":
		return "AWS_REGION"
	case os.Getenv("AWS_DEFAULT_REGION") != "":
		return "AWS_DEFAULT_REGION"
	case startRegion != "":
		return "state.yaml"
	}
	return "the profile"
}

// homeRelative shortens a path under the home directory to ~/….
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}

// startProfile picks the profile and region the TUI opens with: flags, then
// AWS_PROFILE and AWS_REGION / AWS_DEFAULT_REGION, then the last used profile
// and the region saved for the chosen profile. fromState reports whether the
// profile came from the saved state.
func startProfile(flagProfile, flagRegion string, st *store.Store) (profile, region string, fromState bool) {
	profile = flagProfile
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" && st.LastProfile != "" {
		profile, fromState = st.LastProfile, true
	}
	region = flagRegion
	if region == "" && os.Getenv("AWS_REGION") == "" && os.Getenv("AWS_DEFAULT_REGION") == "" {
		region = st.RegionFor(profile)
	}
	return profile, region, fromState
}

func runDoctor(profile, region string) int {
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()
	clients, err := awscfg.Load(ctx, profile, region)
	var checks []preflight.Check
	if err != nil {
		// Still report binary checks; credential checks need the config.
		checks = preflight.CheckBinaries()
		check := preflight.Check{
			Name:   "AWS config",
			Detail: err.Error(),
			Fix:    "run `aws configure` or set AWS_PROFILE/AWS_REGION",
		}
		if help, ok := profileHelp(err); ok {
			check.Detail, check.Fix = help[0], strings.Join(help[1:], "; ")
		}
		checks = append(checks, check)
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
