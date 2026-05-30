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

The version is tag-driven (`git describe` / GoReleaser) — nothing to bump in code. Cut a release by pushing a **strict semver** tag:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow then builds all-OS binaries (linux, darwin, windows × amd64, arm64), generates `checksums.txt`, and publishes a GitHub Release with an auto-generated changelog. Only tags matching `vX.Y.Z` trigger a release.

### Who can release

Only maintainers with write access to this repository can push `vX.Y.Z` tags, and pushing such a tag is what triggers a release. Contributors working from a fork **cannot** trigger releases — propose your change as a PR, and a maintainer cuts the release once it lands on `main`.

## Good first issues

New to the project? Look for issues labelled **`good first issue`** (small, well-scoped) and **`help wanted`** (we'd welcome a hand). Comment on an issue to have it assigned to you before starting, so two people don't duplicate work.

## Reporting security issues

Do **not** open a public issue for vulnerabilities. Follow the process in [SECURITY.md](SECURITY.md) (private reporting via GitHub Security advisories).
