# Developer Guide

For build setup, test commands, and contribution workflow, see
[`CONTRIBUTING.md`](../CONTRIBUTING.md) at the repo root.

## Prerequisites

- Bazelisk or the Bazel version pinned in [`.bazelversion`](../.bazelversion).
  Bazel supplies the Go SDK declared in [`go.mod`](../go.mod).
- Linux for full CI validation and hardware collection. macOS can run supported
  unit tests, but Linux-only code is excluded.

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

The repo is a single Go module with one `go.mod` and one version. Its packages
are grouped by directory:

- **`catalog/`** — check catalog and CEL evaluation engine (no external deps)
- **`toolkit/`** — NVML backend, sensors, and the `health-validation` CLI
- **`collect/`** — telemetry collection emitting protobuf observations
- **`agentkit/`** — gRPC/HTTP server exposing `collect`

The [support lifecycle](../SUPPORT.md#support-lifecycle) applies to the
module as a whole. `collect` and `agentkit` remain experimental and outside that
lifecycle. A dependency pin affects every package, so it can change the versions
linked into the CLI. CI gates `mod tidy -diff`.

Run `bazel run //tools/go -- mod tidy` from the repository root after changing
imports.

Regenerate collection protobufs with `bash collect/hack/genproto.sh`. The script
pins both plugins and leaves message layouts as generated. The generated
`observation.pb.go` is excluded from `fieldalignment` because protobuf requires
`MessageState` as the first field. Run the pinned formatter afterward and verify
binary and protobuf JSON round trips. Linux Bazel tests remain the authoritative
check for the other pinned nogo analyzers.


## Versioning and Releases

The module follows Semantic Versioning 2.0.0. A release tag is the version,
`vX.Y.Z`, and versions the whole repository.

- MAJOR identifies an incompatible change to its public API.
- MINOR identifies backward-compatible functionality.
- PATCH identifies a backward-compatible bug fix.

Before version 1.0.0, project policy uses a MINOR increment for a breaking
public API change and a PATCH increment for a compatible fix. A MINOR release
that deprecates public API must document a migration path. Keep the deprecated
API until a later breaking release removes or changes it.

Prerelease tags use the form `v1.2.3-rc.1`. A prerelease has lower precedence than its associated
normal version and can change before the normal release. Semantic Versioning
build metadata, such as `+build.7`, does not affect version precedence. Release
tags must not include build metadata. The
Go command treats such a tag as non-canonical and can resolve its commit as a
pseudo-version.

The current module path has no major-version suffix and supports v0 and v1.
Before a v2 or later release, change the `go.mod` module path and all of its
imports to the `/vN` suffix.

Land the change on `main`, test it, and tag the release. See
[RELEASE.md](../RELEASE.md) for the RC/QA process, release stamping and image
publishing, and Maintenance backports. The
[supported release lines](../SUPPORT.md#supported-release-lines) track fix
eligibility separately from the pre-1.0 compatibility policy above.

## Documentation Site

`docs/` holds the Markdown pages and `docs/index.yml` (the sidebar order).
`fern/` holds the Fern configuration. Install the CLI with
`npm install -g fern-api@latest`; it is intentionally unpinned.

```bash
fern check               # validate the Fern configuration
fern docs md check       # validate the Markdown/MDX
fern docs dev            # live preview on http://localhost:3000/
```

The published site is versioned by release. A final `vX.Y.Z` tag, or a
manual run of the Fern Docs publish workflow, rewrites the `versions` list in
`fern/docs.yml` (`tools/fern_docs_versions.py`) to one Fern `ref:` version per
MAJOR.MINOR line, built from that line's highest final tag. The newest line is
the default version. Keep the committed list as the single `Latest` entry: each
tag carries it, and Fern builds a ref-backed version from that tag's own default
version. A manual run rebuilds those released versions, so a docs fix on
`main` is published only when a release contains it; only site-wide settings in
`fern/` take effect from `main`. `fern docs dev` previews only the working tree.
Until a release tag contains the Fern site, the site publishes from `main`.

`docs/reference/` (the Check Catalog and Verdict Schema pages) is generated from
`catalog/` and committed. After changing a check or a verdict type, run
`bazel run //docs:reference_update`; `bazel test //...` fails when the committed
pages are stale. A new check category also needs a page entry in
`docs/index.yml`.

Pages that mention `{`, a bare `<`, or HTML comments in prose break the MDX
parser. `tools/check-docs-mdx docs/` finds them, and `--fix` repairs the
mechanical cases. Links to files outside `docs/` do not resolve in Fern, so use
a GitHub URL for those.

## Linux-only Code

macOS tests do not exercise files guarded by `//go:build linux`. Use a Linux VM
or container to validate those paths with the CI configuration. GPU hardware
collection also requires the NVIDIA driver and NVML; the stock CLI's hardware
collection is unavailable on macOS.

### Build a Linux image from macOS

Docker can run Bazel in a Linux/amd64 container and write a loadable image
tarball to `dist/`. On Apple Silicon, configure Docker or Colima with amd64
emulation first.

```shell
docker run --rm --platform=linux/amd64 --entrypoint sh -v "$PWD:/src" -w /src gcr.io/bazel-public/bazel:9.2.0 -c 'bazel build --config=ci --platforms=//build/platforms:linux_amd64 //toolkit/cmd/health-validation:health-validation.image.tar && mkdir -p /src/dist && cp -L bazel-bin/toolkit/cmd/health-validation/health-validation.image.local_load/tarball.tar /src/dist/health-validation-linux-amd64.tar'
```

Copy `dist/health-validation-linux-amd64.tar` to a Linux host and load it with
`docker load -i`; `dist/` is intentionally ignored by Git.

## Building Container Images from Source

Run these commands from the repository root on a Linux build host:

```shell
bazel run --config=ci --platforms=//build/platforms:linux_amd64 \
  //toolkit/cmd/health-validation:health-validation.image.local_load
```

The target builds the image and loads it into Docker as
`health-validation:latest`. For ARM64 nodes, replace `linux_amd64` with
`linux_arm64`. Select the platform for your deployment nodes.

To export a loadable tarball instead:

```shell
bazel build --config=ci --platforms=//build/platforms:linux_amd64 \
  //toolkit/cmd/health-validation:health-validation.image.tar
docker load -i bazel-bin/toolkit/cmd/health-validation/health-validation.image.local_load/tarball.tar
```

You can copy that tarball to another Linux host and load it there. For a macOS
build host, use the Docker recipe in [Build a Linux image from macOS](#build-a-linux-image-from-macos).

Local builds are unstamped by default and report the package's `dev` version.
To embed the selected source revision's version and commit metadata, use
`--config=release-ci` in place of `--config=ci` from a checkout with the complete
tag history. Inspect the result on a host matching the image architecture, or
with emulation configured for that architecture:

```shell
docker run --rm health-validation:latest version
```

For deployment through a registry you control, set `image_repository` to your
repository path and `image_tag` to a tag identifying your source revision, then
tag and push your build:

```shell
image_repository='registry.example.com/your-team/health-validation'
image_tag='your-source-revision'
docker tag health-validation:latest "${image_repository}:${image_tag}"
docker push "${image_repository}:${image_tag}"
```

Choose a tag that identifies your source revision and use the resulting image
reference in your deployment manifests. The build target assigns the local
`latest` tag.
