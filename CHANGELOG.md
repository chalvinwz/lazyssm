# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- CI: gate the `build` matrix behind `test`, `lint`, and `govulncheck` so
  release artifacts only build after all checks pass ([#4]).
- CI: bump `actions/checkout` 4 → 5 ([#5]), `actions/setup-go` 5 → 6 ([#3]),
  and `golangci/golangci-lint-action` 8 → 9 ([#1]).

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

[Unreleased]: https://github.com/chalvinwz/lazyssm/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/chalvinwz/lazyssm/releases/tag/v0.1.0
[#1]: https://github.com/chalvinwz/lazyssm/pull/1
[#3]: https://github.com/chalvinwz/lazyssm/pull/3
[#4]: https://github.com/chalvinwz/lazyssm/pull/4
[#5]: https://github.com/chalvinwz/lazyssm/pull/5
