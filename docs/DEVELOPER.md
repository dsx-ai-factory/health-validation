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
