# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `--auto-login` flag (and persistent `auto_login: true` config option) — when
  the SSO session is expired at startup or during an inventory refresh, lazyssm
  suspends the TUI, runs `aws sso login` with the active profile/region, and
  resumes with a fresh fetch. One attempt per expiry; on failure the preflight
  screen appears as before ([#18]).

### Fixed

- A Name prefix in the filter bar that is shadowed by a later token is now
  reported in the status line, matching how malformed `tag:` tokens are already
  surfaced, instead of being silently dropped.

## [0.2.0] - 2026-05-31

### Added

- Demo mode (`LAZYSSM_DEMO=1`) — runs the TUI against a built-in sample inventory
  with no AWS account or network calls, used to record the README demo GIF.

### Changed

- Connecting now reuses the cached preflight result instead of re-probing the
  AWS CLI and session-manager-plugin binaries on the UI thread.
- Expired/invalid SSO detection now also matches typed AWS error codes
  (`ExpiredToken` / `ExpiredTokenException`) in addition to the text heuristic.
- CI: gate the `build` matrix behind `test`, `lint`, and `govulncheck` so
  release artifacts only build after all checks pass ([#4]).
- CI: bump `actions/checkout` 4 → 5 ([#5]), `actions/setup-go` 5 → 6 ([#3]),
  and `golangci/golangci-lint-action` 8 → 9 ([#1]).
- CI: the `test` job prints a coverage summary and uploads `coverage.out` as a
  build artifact.

### Fixed

- AWS API calls now run under deadlines (30s inventory fetch, 15s preflight and
  startup config load) so a wedged endpoint can no longer leave the TUI spinning
  indefinitely.
- Rapid refresh / source-toggle no longer lets a slower, older inventory fetch
  overwrite a newer result — replies are matched to the request that issued them.
- Malformed filter tokens (e.g. `tag:Env` with no `=value`) are reported in the
  status line instead of being silently dropped.
- Fetch errors are surfaced distinctly while the last successful instance list is
  kept on screen rather than blanked.

## [0.1.0] - 2026-05-30

Initial public release.

### Added

- Persistent-panel Bubble Tea TUI for AWS SSM Session Manager — the instance
  list stays open between sessions instead of dropping back to the shell.
- Merged inventory joining SSM `DescribeInstanceInformation` with EC2
  `DescribeInstances` (Name tags, IPs, instance state alongside SSM agent
  status).
- Server-side tag/name filter (`f`) — `tag:K=V` and Name-prefix tokens pushed
  to the EC2/SSM APIs with AND semantics.
- Client-side fuzzy search (`/`) over the fetched set's Name tags, with live
  navigation of narrowed results.
- Toggle source (`t`) between SSM-managed nodes only and all EC2 instances.
- Pin favorites (`p`) persisted as YAML at `os.UserConfigDir()/lazyssm`;
  pinned instances float to the top on launch.
- One-key connect (`Enter`) handing the terminal to `aws ssm start-session`,
  resuming the TUI when the session ends.
- `doctor` subcommand — preflight checks for the AWS CLI, session-manager-plugin,
  region resolution, credentials, and SSO session validity.
- `--profile` / `--region` global flags and `--version` output.
- GoReleaser cross-build for linux, darwin, and windows on amd64 and arm64.

[Unreleased]: https://github.com/chalvinwz/lazyssm/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/chalvinwz/lazyssm/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/chalvinwz/lazyssm/releases/tag/v0.1.0
[#1]: https://github.com/chalvinwz/lazyssm/pull/1
[#3]: https://github.com/chalvinwz/lazyssm/pull/3
[#4]: https://github.com/chalvinwz/lazyssm/pull/4
[#5]: https://github.com/chalvinwz/lazyssm/pull/5
[#18]: https://github.com/chalvinwz/lazyssm/issues/18
