# Release Process

## What gets versioned

The repository publishes two independently versioned Go modules — `catalog`
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

## Supported release lines and backports

[SUPPORT.md](SUPPORT.md#support-lifecycle) defines the Current, Maintenance,
and EOL stages for each module, including the pre-1.0 policy and the
three-month previous-major Maintenance tail. Catalog lines required by a
supported toolkit line remain eligible for security and critical fixes needed
by that toolkit line while it is supported.

When publishing a final release, update the
[supported release lines](SUPPORT.md#supported-release-lines) and identify
stage transitions, EOL dates for previous-major tails, and migration guidance
in the release notes. Recheck the initial covered lines when adopting this
policy if another final release has shipped during review.

Maintainers evaluate security and critical bug fixes for Maintenance lines on
a best-effort basis. Security fixes follow the private coordination and
disclosure process in [SECURITY.md](SECURITY.md). When maintainers choose a
Maintenance backport, critical bug fixes applicable to `main` land there first,
then are backported and validated on the affected release line before its
patch release. Security and critical bug fixes specific to a Maintenance line
may be fixed on that release branch. Do not include new features or other bug
fixes in a Maintenance release.

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

**Before 1.0.0** (both modules are currently `v0.x`), project policy departs
from the scheme above: a breaking public-API change bumps **MINOR** instead of
MAJOR, and a compatible fix bumps **PATCH**. A MINOR release that deprecates
public API must document a migration path and keep the deprecated API until a
later breaking release removes it. See
[`docs/DEVELOPER.md`](docs/DEVELOPER.md#versioning-and-releases) for the full
policy.

## Branch naming and tagging

Tag each module independently with its module name as a prefix:

```text
catalog/vX.Y.Z
toolkit/vX.Y.Z
```

Release candidates use a `-rcN` suffix (e.g. `toolkit/v1.2.0-rc1`); commits
after an RC tag continue to version as prereleases of that same final version
until the final tag lands.

A minor release goes through QA as a toolkit RC, without freezing `main`:

1. Tag `catalog/vX.Y.0` on `main`.
2. Merge a PR to `main` that bumps `toolkit/go.mod`'s catalog `require` to
   `vX.Y.0` and adds the `CHANGELOG.md` entry. Point the entry's links at the
   final `vX.Y.0` tags, even though the toolkit one does not exist yet.
3. Before tagging, confirm `catalog/` is unchanged since the catalog tag
   (`git diff --quiet catalog/vX.Y.0 <commit> -- catalog`). If it changed, the
   image reports a catalog pseudo-version instead of `vX.Y.0`.
4. Tag that commit `toolkit/vX.Y.0-rc1`, and create `release/vX.Y` from the
   tag.
5. Land QA fixes on `main` first, then cherry-pick them onto `release/vX.Y`
   and tag the next `-rcN` there. A catalog fix ships as `catalog/vX.Y.1`,
   with the release branch's catalog `require` bumped to match.
6. Tag `toolkit/vX.Y.0` on `release/vX.Y` once QA passes. Later patch
   releases for that line are tagged on the same branch.

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
unstamped and reports the package's "dev" defaults. CI's `release-metadata`
job resolves the versions once, and every later stamped build in the run reuses
them, so a tag pushed mid-run cannot split an image's tag from its embedded
version.

No binary/archive artifacts are attached to GitHub releases — the container
image is the only published release artifact today. There are no user-facing
release notes generated automatically; summarize notable changes in the tag's
GitHub release description.
