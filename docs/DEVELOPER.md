# Developer Guide

For build setup, test commands, and contribution workflow, see
[`CONTRIBUTING.md`](../CONTRIBUTING.md) at the repo root.

## Prerequisites

- Go ≥ 1.23
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

## Module Layout

The repo contains two independently versioned Go modules:

- **`catalog/`** — check catalog and CEL evaluation engine (no external deps)
- **`toolkit/`** — NVML backend, sensors, and the `health-validation` CLI

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
