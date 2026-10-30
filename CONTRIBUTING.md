# Contributing to lazyssm

## Trunk-based workflow

`main` is always releasable. **Never push directly to `main`** — every change lands through a short-lived branch and a pull request.

```
main ──●─────────────●──────────●──  (always green, always releasable)
        \           /          /
         feature/x         fix/y      (short-lived, squash-merged)
```

## Branch naming

Name branches `prefix/kebab-summary`, where `prefix` is one of:

| Prefix      | Use for                          |
|-------------|----------------------------------|
| `feature/`  | new functionality                |
| `fix/`      | bug fixes                        |
| `hotfix/`   | urgent production fixes          |
| `chore/`    | maintenance, deps, tooling       |
| `ci/`       | CI / build pipeline changes      |
| `docs/`     | documentation only               |
| `refactor/` | internal restructure, no behavior change |
| `test/`     | tests only                       |

Examples: `ci/add-golangci-lint`, `fix/natural-sort`, `feature/instance-search`.

## Before opening a PR

Run the local gate — it must pass:

```sh
make check        # gofmt -l, go vet, go test
```

CI re-runs this plus a race detector, `golangci-lint`, `govulncheck`, and a 6-target cross-build. **All CI checks must be green before merge.**

## Merging

Squash-merge only, keeping `main` history linear. As the sole maintainer you self-merge once CI is green; no separate approval is required.

## Releasing

Releases are cut by [release-please](https://github.com/googleapis/release-please).
Every merge to `main` rebuilds one open release PR, `chore: release vX.Y.Z`,
from the latest `main` (`always-update` in `release-please-config.json`), so it
never lags behind. It holds:

- the next version, worked out from the merged PR titles: `feat:` bumps the
  minor version and `fix:` the patch (before 1.0, a breaking change bumps the
  minor version too)
- that version's `CHANGELOG.md` section, one line per PR title
- `docs/demo.gif`, re-recorded from `demo/lazyssm.tape` after every merge

Merging the release PR tags `vX.Y.Z` and publishes the GitHub release. The tag
runs the `release` workflow, which builds all-OS binaries (linux, darwin,
windows × amd64, arm64) with GoReleaser, uploads them with `checksums.txt`, and
publishes the Homebrew formula and Scoop manifest. Nothing in the code is
bumped; the version comes from the tag.

PR titles become the changelog, so write them for users: `feat:` for something
new they can do, `fix:` for a bug they hit, `build(deps):` for dependency
updates. `chore:`, `ci:`, `docs:`, `refactor:`, `style:` and `test:` are left
out of the changelog.

### Release token

release-please uses the `RELEASE_PLEASE_TOKEN` secret: a fine-grained personal
access token for this repository with **Contents**, **Pull requests** and
**Issues** set to read and write (release-please labels its PRs). It can't use
the built-in `GITHUB_TOKEN`, because GitHub runs no workflows for PRs and tags
that token creates: the release PR would get no CI and the tag wouldn't start
the `release` workflow. Renew the token before it expires; an expired token
stops the release PR from updating.

### Who can release

Only maintainers with write access to this repository can merge the release PR, and merging it is what triggers a release. Contributors working from a fork **cannot** trigger releases — propose your change as a PR, and a maintainer cuts the release once it lands on `main`.

## Good first issues

New to the project? Look for issues labelled **`good first issue`** (small, well-scoped) and **`help wanted`** (we'd welcome a hand). Comment on an issue to have it assigned to you before starting, so two people don't duplicate work.

## Reporting security issues

Do **not** open a public issue for vulnerabilities. Follow the process in [SECURITY.md](SECURITY.md) (private reporting via GitHub Security advisories).
