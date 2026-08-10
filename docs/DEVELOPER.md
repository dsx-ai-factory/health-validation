# Developer Guide

For build setup, test commands, and contribution workflow, see
[`CONTRIBUTING.md`](../CONTRIBUTING.md) at the repo root.

## Prerequisites

- Go ≥ 1.23
- Bazel ≥ 9 (pinned version in `.bazelversion`)
- Linux for NVML-dependent targets (`toolkit/nvml/`); macOS works for catalog and CLI logic

## Quick Start

```bash
git clone https://github.com/NVIDIA/health-validation.git
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
and `health-validation run` exits with a backend error. Use RBE or a Linux
VM to test NVML-dependent paths.
