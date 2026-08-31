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
