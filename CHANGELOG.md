# Changelog

All notable changes to this project are documented in this file, and the
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

From 0.5.0 on, release-please writes each version's section from the titles of
the pull requests merged since the last release; each line links to its PR for
the details. Earlier versions follow
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.5.0](https://github.com/chalvinwz/lazyssm/compare/v0.4.0...v0.5.0) (2026-10-30)


### Added

* switch AWS profile and region in the TUI ([e48f21a](https://github.com/chalvinwz/lazyssm/commit/e48f21a4a1b4ec51bc5c671eeae5c9709c66cf6e))

## [0.4.0] - 2026-10-30

### Added

- Instance age and colored EC2 state in the detail sidebar; configurable list
  columns (`columns` in the config file: `name`, `ip`, `state`, `age`, `ssm`,
  `id`, `os`, `agent`, `ping`); theme presets (`theme` in the config file, or
  `--theme`: `default`, `tokyo-night`, `dracula`, `nord`, `catppuccin-mocha`);
  and `transparent_background` ([#33]).

### Changed

- The inventory fetch asks SSM for 50 nodes per page instead of the default 10,
  and queries SSM and EC2 at the same time ([#38]).
- Dependencies: Bubbles 2.2.1, Bubble Tea 2.1.0, Lip Gloss 2.0.6,
  aws-sdk-go-v2/config 1.33.9, smithy-go 1.28.6 ([#36]).

### Fixed

- Typing in the search bar moves the highlight to the best match. It used to
  stay on the previous row number, so `Enter` could connect to a weaker match
  ([#38]).
- Pinning, unpinning and refreshing keep the same instance selected instead of
  the same row number, so a second `p` no longer lands on a different instance
  ([#38]).
- SSM-only browsing works without `ec2:DescribeInstances`, as the README already
  said: nodes are listed by hostname and the status line names what is missing.
  It used to fail the whole fetch ([#38]).
- Sessions open in the region lazyssm resolved for the inventory on screen, not
  only the one given with `--region` ([#38]).
- On-premises and other non-EC2 nodes (`mi-…`) show their registered name or
  hostname instead of "(no Name)" ([#38]).

## [0.3.0] - 2026-10-25

### Added

- `--auto-login` flag (and persistent `auto_login: true` config option) — when
  the SSO session is expired at startup or during an inventory refresh, lazyssm
  suspends the TUI, runs `aws sso login` with the active profile/region, and
  resumes with a fresh fetch. One attempt per expiry; on failure the preflight
  screen appears as before ([#18]).
- `y` / `Y` copy the selected instance's ID / IP to the clipboard. Locally
  lazyssm uses the system copy tool (`pbcopy`, `clip.exe`, `wl-copy`, `xclip`
  or `xsel`); over SSH, or when none is installed, it uses the terminal's
  OSC 52 clipboard instead. Each copy uses exactly one of the two, so
  clipboard-history tools record a single entry ([#34]).
- One-command installs: `install.sh` (macOS, Linux, WSL) and `install.ps1`
  (Windows) download the release archive and verify it against the release's
  `checksums.txt` before installing, plus a Homebrew tap
  (`brew install chalvinwz/tap/lazyssm`) and a Scoop bucket
  (`scoop bucket add chalvinwz https://github.com/chalvinwz/scoop-bucket`).
  `LAZYSSM_VERSION` pins a release and `LAZYSSM_INSTALL_DIR` picks the install
  directory ([#35]).

### Changed

- Dependencies: Bubble Tea 2.0.8, Bubbles 2.1.1, Lip Gloss 2.0.5, fuzzy 0.1.3,
  and current AWS SDK for Go v2 modules.
- CI: every GitHub Action is pinned to a commit SHA; `actions/checkout` 5 → 7
  ([#17]), `actions/setup-go` 6 → 7 ([#32]), `actions/upload-artifact` 4 → 7
  ([#11]). Dependabot groups updates into one PR per ecosystem ([#35]).
- Releases also publish the Homebrew formula and Scoop manifest, then install
  through every channel and fail unless each reports the tagged version ([#35]).

### Fixed

- govulncheck no longer fails in CI on the embedded checkout bundled with
  `govulncheck-action` ([#20]).
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

[0.4.0]: https://github.com/chalvinwz/lazyssm/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/chalvinwz/lazyssm/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/chalvinwz/lazyssm/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/chalvinwz/lazyssm/releases/tag/v0.1.0
[#1]: https://github.com/chalvinwz/lazyssm/pull/1
[#3]: https://github.com/chalvinwz/lazyssm/pull/3
[#4]: https://github.com/chalvinwz/lazyssm/pull/4
[#5]: https://github.com/chalvinwz/lazyssm/pull/5
[#11]: https://github.com/chalvinwz/lazyssm/pull/11
[#17]: https://github.com/chalvinwz/lazyssm/pull/17
[#18]: https://github.com/chalvinwz/lazyssm/issues/18
[#20]: https://github.com/chalvinwz/lazyssm/pull/20
[#32]: https://github.com/chalvinwz/lazyssm/pull/32
[#33]: https://github.com/chalvinwz/lazyssm/pull/33
[#34]: https://github.com/chalvinwz/lazyssm/pull/34
[#35]: https://github.com/chalvinwz/lazyssm/pull/35
[#36]: https://github.com/chalvinwz/lazyssm/pull/36
[#38]: https://github.com/chalvinwz/lazyssm/pull/38
