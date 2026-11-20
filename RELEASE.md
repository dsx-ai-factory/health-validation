# Release Process

## What gets versioned

The repository is a single Go module, `github.com/dsx-ai-factory/health-validation`,
released under one version: one `go.mod`, one `vX.Y.Z` tag. That tag versions
every package in the repository (`catalog`, `toolkit`, `collect`, and
`agentkit`) together, and also stamps the multi-architecture
(`linux/amd64` + `linux/arm64`) container image built from
`toolkit/cmd/health-validation`. Users build deployment images from their
selected source revision; see
[Building Container Images from Source](docs/developer.md#building-container-images-from-source).

## Cadence

There is no fixed release cadence. A release is cut when there's a meaningful
set of merged changes to ship — typically after one or more feature/fix PRs
land on `main` — not on a calendar schedule.

## Supported release lines and backports

[SUPPORT.md](SUPPORT.md#support-lifecycle) defines the Current, Maintenance,
and EOL stages for each release line, including the pre-1.0 policy and the
three-month previous-major Maintenance tail.

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

The module follows [Semantic Versioning](https://semver.org/): `MAJOR.MINOR.PATCH`.

- **MAJOR** — a breaking change to the module's public Go API or the CLI's
  behavior/flags. For the verdict JSON contract
  specifically, `schema_version` (documented in
  [`catalog/health/node-verdict.schema.json`](catalog/health/node-verdict.schema.json))
  is the authoritative breaking-change signal, independent of the module's own
  semver.
- **MINOR** — backward-compatible additions (a new check, a new optional field,
  a new exported function).
- **PATCH** — backward-compatible bug fixes.

**Before 1.0.0** (the module is currently `v0.x`), project policy departs
from the scheme above: a breaking public-API change bumps **MINOR** instead of
MAJOR, and a compatible fix bumps **PATCH**. A MINOR release that deprecates
public API must document a migration path and keep the deprecated API until a
later breaking release removes it. See
[`docs/developer.md`](docs/developer.md#versioning-and-releases) for the full
policy.

## Branch naming and tagging

Tag a release with its semantic version:

```text
vX.Y.Z
```

Release candidates use a `-rc.N` suffix (e.g. `v1.2.0-rc.1`); commits after an RC
tag continue to version as prereleases of that same final version until the
final tag lands.

A minor release goes through QA as an RC, without freezing `main`:

1. Merge a PR to `main` that adds the `CHANGELOG.md` entry. Point the entry's
   link at the final `vX.Y.0` tag, even though it does not exist yet.
2. Tag that commit `vX.Y.0-rc.1`, and create `release/vX.Y` from the tag.
3. Land QA fixes on `main` first, then cherry-pick them onto `release/vX.Y`
   and tag the next `-rc.N` there.
4. Tag `vX.Y.0` on `release/vX.Y` once QA passes. Later patch releases for that
   line are tagged on the same branch.

Because the module lives at the repository root, `go get
github.com/dsx-ai-factory/health-validation@vX.Y.Z` resolves the tag directly.

## Retired per-module tags

`catalog/vX.Y.Z`, `collect/vX.Y.Z`, and `toolkit/vX.Y.Z` tags belong to the
modules that were merged into the root module. They remain on the Go module
proxy, and Go prefers the longest matching module path, so they would otherwise
shadow the root module: `go install …/toolkit/cmd/health-validation@latest`
picks `toolkit@v0.3.2`, whose `go.mod` has `replace` directives that
`go install` rejects.

One final version of each retired module retracts every earlier version:
`catalog/v0.3.99`, `collect/v0.3.99`, and `toolkit/v0.3.99`. Each tag points at
a commit outside `main` and every release branch, holding only that module's
`go.mod` and no `.github/` directory:

```text
// Deprecated: merged into github.com/dsx-ai-factory/health-validation; depend on that module instead.
module github.com/dsx-ai-factory/health-validation/toolkit

// No code here; 1.16 is the minimum that supports retract.
go 1.16

// Merged into github.com/dsx-ai-factory/health-validation.
retract [v0.0.0-0, v0.3.99]
```

`catalog` and `collect` use their own module paths.

The tags are `v0.3.99`, not the next patch number, because Go reads retractions
from a module's highest version. A real `toolkit/v0.3.N` patch tagged above the
retraction would be `@latest` and carry no `retract`, bringing the failure back.
Patches on `release/v0.3` stay below `v0.3.99`, so the retraction stays
authoritative, and they fall inside the retracted range. Never require a
`v0.3.99` version from `release/v0.3`: it holds no code.

Do not add a `go.mod` under `catalog/`, `collect/`, or `toolkit/` on `main` or a
release branch: it would split the root module again. `release/v0.3` already
carries them, since it predates the merge. The retraction tags match
`release/v0.3`'s `catalog/v*` and `toolkit/v*` CI triggers, which publish an
image on `toolkit/v*`. Workflows run from the tagged commit, so the commit must
not contain `.github/`.

Retraction does not break builds that pin a retired version, including patches
of the `v0.3` line; it only flags them and stops `@latest` and `go get -u` from
choosing them. Consumers who stay on the `v0.3` line must request each patch by
its explicit version (`go get …/toolkit@v0.3.4`) or move to the root module.
Those patches carry the "Merged into …" retraction notice even though they are
supported releases.

Retraction does not affect explicit revision queries. `go install …@main` still
resolves `…/toolkit` to a pre-merge pseudo-version, so it cannot be fixed this
way, and tagging a root release does not change it. Install the tip by pinning
the root module's version, as the README shows.

## Who can trigger a release

Any [maintainer](MAINTAINERS.md) can cut a release by pushing a tag in the
format above. See [`GOVERNANCE.md`](GOVERNANCE.md) for how maintainers are
added.

## What CI does automatically

Pushing a `vX.Y.Z` (or `-rc.N`) tag, or merging to `main`, runs the full
`.github/workflows/ci.yml` pipeline: lint, test, build and smoke-test the image
on both architectures, and a Grype vulnerability scan that blocks on any fixable
Critical. After these checks pass, `publish-image` publishes a multi-architecture
OCI index for internal use. Build deployment images using
[Building Container Images from Source](docs/developer.md#building-container-images-from-source).

The version and commit metadata baked into each image (and reported by
`health-validation version`) comes from `tools/workspace_status_release.sh`,
which CI invokes via `--config=release-ci`; a plain local build stays
unstamped and reports the package's "dev" defaults. CI's `release-metadata`
job resolves the version once, and every later stamped build in the run reuses
it, so a tag pushed mid-run cannot split an image's tag from its embedded
version.

A final `vX.Y.Z` tag also runs `.github/workflows/fern-docs-publish.yml`,
which republishes the docs site with one version per MAJOR.MINOR line. Fern
builds each version from that line's highest final tag, so a release's docs are
the docs at its tag. The newest line is the default; older lines stay available
under `/vX.Y/`. Release candidates do not publish docs.

A docs fix merged to `main` reaches the site only in a release that contains it:
the next release, or a patch release on the affected `release/vX.Y` branch. A
manual run of the workflow rebuilds the released versions from their tags, so
it does not publish page changes from `main`. It only applies site-wide
configuration from `main`'s `fern/` directory, such as the theme, navbar, and
redirects.

No binary/archive artifacts are attached to GitHub releases. There are no user-facing
release notes generated automatically; summarize notable changes in the tag's
GitHub release description.
