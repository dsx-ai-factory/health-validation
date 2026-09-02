# Release Process

## What gets versioned

The repository is split into two independently versioned Go modules — `catalog`
and `toolkit` — plus a multi-architecture (`linux/amd64` + `linux/arm64`)
container image built from `toolkit/cmd/health-validation` and published to
[GHCR](https://ghcr.io/dsx-ai-factory/health-validation). `catalog` and
`toolkit` are tagged separately; `toolkit` depends on `catalog`, so a `catalog`
release that changes its public API is usually followed by a `toolkit` release
that picks it up. Only `toolkit` tags publish a container image, since the
image packages the `toolkit/cmd/health-validation` CLI.

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

Releases are cut from `main`; there is no separate release branch. Tag each
module independently with its module name as a prefix:

```text
catalog/vX.Y.Z
toolkit/vX.Y.Z
```

Release candidates use a `-rcN` suffix (e.g. `toolkit/v1.2.0-rc1`); commits
after an RC tag continue to version as prereleases of that same final version
until the final tag lands.

This matches Go's module-tagging convention for a multi-module repository, so
`go get github.com/dsx-ai-factory/health-validation/catalog@vX.Y.Z` resolves
correctly.

## Who can trigger a release

Any [maintainer](MAINTAINERS.md) can cut a release by pushing a tag in the
format above. See [`GOVERNANCE.md`](GOVERNANCE.md) for how maintainers are
added.

## What CI does automatically

Pushing a `toolkit/vX.Y.Z` (or `-rcN`) tag, or merging to `main`, runs the full
`.github/workflows/ci.yml` pipeline: lint, test, build and smoke-test the image
on both architectures, and a Grype vulnerability scan that blocks on any fixable
Critical. If all of that passes, `publish-image` builds the multi-architecture
OCI index and pushes it to `ghcr.io/dsx-ai-factory/health-validation`:

- **`toolkit/vX.Y.Z` tag** → publishes only the immutable `X.Y.Z` tag.
- **`main`** → publishes an immutable pseudo-version tag (derived from the last
  toolkit release, commit time, and short SHA) and moves `latest`.

The version and commit metadata baked into each image (and reported by
`health-validation version`) comes from `tools/workspace_status_release.sh`,
which CI invokes via `--config=release-ci`; a plain local build stays
unstamped and reports the package's "dev" defaults.

No binary/archive artifacts are attached to GitHub releases — the container
image is the only published release artifact today. There are no user-facing
release notes generated automatically; summarize notable changes in the tag's
GitHub release description.
