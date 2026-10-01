# Developer Guide

For build setup, test commands, and contribution workflow, see
[`CONTRIBUTING.md`](../CONTRIBUTING.md) at the repo root.

## Prerequisites

- Go ≥ 1.27
- Bazel ≥ 9 (pinned version in `.bazelversion`)
- Linux for NVML-dependent targets (`toolkit/nvml/`); macOS works for catalog and CLI logic

## Quick Start

```bash
git clone https://github.com/dsx-ai-factory/health-validation.git
cd health-validation

bazel build //...       # build everything
bazel test //...        # run all tests
bazel run //tools/format  # format (gofumpt + buildifier)
bazel run //tools/gazelle # sync BUILD files after import changes
```

## Shipped module inventory

`toolkit/cmd/health-validation/SHIPPED_MODULES.txt` records the third-party Go
modules the linker embeds in the shipped `linux/amd64` binary. It is generated,
not hand-edited:

```bash
bazel run //toolkit/cmd/health-validation:shipped_modules_update
```

`bazel test //...` fails when it drifts from the binary, and
`//toolkit/cmd/health-validation:shipped_modules_cross_check` separately checks it
against the module set the Bazel graph says is linked.

`NOTICE` records the same dependency versions and is gated by its own check:

```bash
bazel run //tools:check_notice              # check
bazel run //tools:check_notice -- --write   # regenerate, then review the diff
```

Regenerate both files in the same change as any dependency bump that moves a
linked version.

## Module Layout

The repo contains four Go modules, joined by a root `go.work`:

- **`catalog/`** — check catalog and CEL evaluation engine (no external deps)
- **`toolkit/`** — NVML backend, sensors, and the `health-validation` CLI
- **`collect/`** — telemetry collection emitting protobuf observations
- **`agentkit/`** — gRPC/HTTP server exposing `collect`

Only `catalog` and `toolkit` are independently versioned and published. All four
are resolved together by Gazelle, so a dependency pin in any of them can change
the versions linked into the CLI. CI gates `mod tidy -diff` on all four.

Run `go mod tidy` inside each module directory after changing imports.

## Versioning and Releases

The `catalog` and `toolkit` modules follow Semantic Versioning 2.0.0
independently. Each release tag prefixes the module's version:

- `catalog/vX.Y.Z`
- `toolkit/vX.Y.Z`

The module versions do not need to match. For each module:

- MAJOR identifies an incompatible change to its public API.
- MINOR identifies backward-compatible functionality.
- PATCH identifies a backward-compatible bug fix.

Before version 1.0.0, project policy uses a MINOR increment for a breaking
public API change and a PATCH increment for a compatible fix. A MINOR release
that deprecates public API must document a migration path. Keep the deprecated
API until a later breaking release removes or changes it.

Prerelease tags use the scoped forms `catalog/v1.2.3-rc.1` and
`toolkit/v1.2.3-rc.1`. A prerelease has lower precedence than its associated
normal version and can change before the normal release. Semantic Versioning
build metadata, such as `+build.7`, does not affect version precedence. The
catalog and toolkit Go module release tags must not include build metadata. The
Go command treats such a tag as non-canonical and can resolve its commit as a
pseudo-version.

The current module paths have no major-version suffix and support v0 and v1.
Before a v2 or later release, change the applicable `go.mod` module path and all
of its imports to the `/vN` suffix. The scoped tags remain, for example,
`catalog/v2.0.0` and `toolkit/v2.0.0` after the `/v2` migration.

When a toolkit release needs a new catalog version, tag the catalog release
first. Then update the toolkit `require` directive to that catalog version,
test the combined modules, and tag the toolkit release. This policy defines
tag and compatibility semantics only. The repository does not currently
provide automatic release stamping or publishing.

## Documentation Site

`docs/` is a Docusaurus site, built through Bazel rather than pnpm directly:

```bash
bazel run //docs:serve    # live preview on http://localhost:3000/
bazel build //docs:build  # one-shot build into bazel-bin/docs/build
```

Most reference content is generated rather than authored, by two separate
paths. `//catalog/cmd/gencheckpages:pages` reads the `Category` enum from
`//catalog/health:schema` and writes one MDX page per category. The checks
themselves never pass through it: `//catalog/cmd/healthdump:checks` lands as
`static/data/checks.json`, which the `health-data` plugin loads and the
`CheckCatalog` component renders into those pages at build time. Either way,
editing a check in `catalog/` changes the published docs. `//docs:test` builds
the whole site and is part of `//...`, so a broken site fails `bazel test //...`.

Pushes to `main` publish the site to GitHub Pages via
[`.github/workflows/pages.yml`](../.github/workflows/pages.yml). The workflow
skips pushes that cannot change the site, so a commit confined to `toolkit/`,
`collect/`, `agentkit/` or the root docs does not redeploy; its `paths-ignore`
list is a denylist, so anything unlisted still deploys. `workflow_dispatch`
republishes on demand. Pages is enabled on this repository with Source set to
GitHub Actions.

The workflow takes `url` and `baseUrl` from `actions/configure-pages`, so the
site is built for wherever GitHub serves it: a generated access-controlled
subdomain while the repository is internal, and
https://dsx-ai-factory.github.io/health-validation/ once it is public. Going
public needs no change here.

## Linux-only Code

`toolkit/nvml/` requires cgo and `libnvidia-ml.so.1`. On macOS, NVML stubs out
and `health-validation run` exits with a backend error. Use a Linux VM to test
NVML-dependent paths.

### Build a Linux image from macOS

Docker can run Bazel in a Linux/amd64 container and write a loadable image
tarball to `dist/`. On Apple Silicon, configure Docker or Colima with amd64
emulation first.

```shell
docker run --rm --platform=linux/amd64 --entrypoint sh -v "$PWD:/src" -w /src gcr.io/bazel-public/bazel:9.2.0 -c 'bazel build --config=ci --platforms=//build/platforms:linux_amd64 //toolkit/cmd/health-validation:health-validation.image.tar && mkdir -p /src/dist && cp -L bazel-bin/toolkit/cmd/health-validation/health-validation.image.local_load/tarball.tar /src/dist/health-validation-linux-amd64.tar'
```

Copy `dist/health-validation-linux-amd64.tar` to a Linux host and load it with
`docker load -i`; `dist/` is intentionally ignored by Git.

## Pulling a Published Image

CI publishes a multi-architecture image for Linux amd64 and arm64:

```shell
docker pull ghcr.io/dsx-ai-factory/health-validation:<tag>
```

| Trigger | Image tags |
|---|---|
| Commit to `main` | `<pseudo-version>` and `latest` |
| Git tag `toolkit/vX.Y.Z` or `toolkit/vX.Y.Z-rcN` | `X.Y.Z` or `X.Y.Z-rcN` |
| Git tag `catalog/vX.Y.Z` or `catalog/vX.Y.Z-rcN` | No image; CI validates the catalog release |

The binary keeps the leading `v`, while the image tag omits it. For example,
`toolkit/v1.2.3` stamps the binary as `v1.2.3` and publishes the image as
`ghcr.io/dsx-ai-factory/health-validation:1.2.3`. Package visibility controls
whether pulling requires `docker login ghcr.io`; private packages require a
token with package read access.

Images use a toolkit release version on toolkit tags and a pseudo-version on
main. They embed the matching catalog release or a catalog pseudo-version when
sources have changed. Existing images remain unchanged; new tags apply to the
next image build. Before publishing, CI runs both architectures and verifies the
embedded toolkit and catalog versions.
