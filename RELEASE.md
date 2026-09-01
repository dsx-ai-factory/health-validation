# Release Process

## What gets versioned

The repository is split into two independently versioned Go modules — `catalog`
and `toolkit` — plus the `health-validation` CLI binary and container image
built from `toolkit/cmd/health-validation`. `catalog` and `toolkit` are tagged
and released separately; `toolkit` depends on `catalog`, so a `catalog` release
that changes its public API is usually followed by a `toolkit` release that
picks it up.

## Cadence

There is no fixed release cadence. A release is cut when there's a meaningful
set of merged changes to ship — typically after one or more feature/fix PRs
land on `main` — not on a calendar schedule.

## Versioning

Both modules follow [Semantic Versioning](https://semver.org/): `MAJOR.MINOR.PATCH`.

- **MAJOR** — a breaking change to the module's public Go API (`catalog`) or
  the CLI's behavior/flags (`toolkit`). For the verdict JSON contract
  specifically, `schema_version` (documented in
  [`catalog/health/node-verdict.schema.json`](catalog/health/node-verdict.schema.json))
  is the authoritative breaking-change signal, independent of the module's own
  semver.
- **MINOR** — backward-compatible additions (a new check, a new optional field,
  a new exported function).
- **PATCH** — backward-compatible bug fixes.

## Branch naming and tagging

Releases are cut from `main`; there is no separate release branch today. Tag
each module independently with its module name as a prefix:

```text
catalog/vX.Y.Z
toolkit/vX.Y.Z
```

This matches Go's module-tagging convention for a multi-module repository, so
`go get github.com/dsx-ai-factory/health-validation/catalog@vX.Y.Z` resolves
correctly.

## Who can trigger a release

Any [maintainer](MAINTAINERS.md) can cut a release by pushing a tag in the
format above. See [`GOVERNANCE.md`](GOVERNANCE.md) for how maintainers are
added.

## Current state and known gaps

Tagging is a manual step today; there is no CI workflow that builds or
publishes release artifacts on tag push (`.bazel/release.bazelrc`'s stamping
config exists but isn't wired to an automated publish job yet — see
`.github/workflows/ci.yml` for what CI does today: format/lint, test, and
build-only). Until that lands:

- Tag the module(s) being released.
- Container images and binaries are not published as release artifacts by CI;
  `docs/DEVELOPER.md` and the `//toolkit/cmd/health-validation` OCI targets
  cover building them locally or in a Linux CI environment.
- There are no user-facing release notes generated automatically. Summarize
  notable changes in the tag's GitHub release description until that's
  automated.
