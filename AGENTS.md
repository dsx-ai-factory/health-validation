# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Agent Scope

### Permitted without asking first

- Reading any file in the repository, running `bazel build`/`bazel test`/`go build`/`go test`, formatting (`bazel run //tools/format`), and regenerating `BUILD.bazel` files (`bazel run //tools/gazelle`).
- Editing tracked source, test, and doc files in a local branch or worktree.
- Creating a local branch and committing to it.

### Requires explicit human confirmation first

- Pushing a branch, opening a PR, or otherwise making a change visible outside your local checkout.
- Merging a PR, tagging a release, or anything that publishes an artifact (container image, binary, module version).
- Any destructive or hard-to-reverse git operation: force-push, `git reset --hard`, rewriting already-pushed history, deleting a branch or tag.
- Changing CI/CD configuration (`.github/workflows/`), branch protection, or repository settings.
- Modifying `LICENSE`, `SECURITY.md`, `GOVERNANCE.md`, or `CODE_OF_CONDUCT.md`.

### Out of scope

- Filing or responding to a security report — route to NVIDIA PSIRT per [`SECURITY.md`](SECURITY.md); do not investigate or fix a suspected vulnerability from an external report without a maintainer directing that work.
- Making governance decisions (adding/removing maintainers, changing decision-making process) — see [`GOVERNANCE.md`](GOVERNANCE.md).
- Deploying to or configuring infrastructure outside this repository (registries, package indexes, hosting).

### Secrets and credentials

- Never commit secrets, tokens, API keys, or credentials, including in test fixtures, config examples, or comments. If you find one already committed, treat it as compromised and flag it to a maintainer immediately rather than just deleting it (history retains it).
- This repository is public: assume anything pushed is public immediately and permanently, even after a force-push or squash. Do not push internal NVIDIA URLs, hostnames, tooling references, or unreleased hardware/product details — the PR template's checklist ([`.github/PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md)) has the exact item to self-check against.
- Do not read from or write to credential stores, `~/.netrc`, cloud provider config, or SSH keys as part of a task in this repository.

## Boundaries

### Capability boundaries

- Health Validation emits verdicts. It does not take action. The binary exits after writing JSON. What happens to the node is the downstream system's decision.
- Health Validation is node-local. Each run evaluates one node's measurements. There is no cross-node analysis, fleet correlation, or pattern detection.
- Health Validation is not a monitoring system. It runs on demand and produces one verdict per invocation. It does not maintain state between runs or stream signals continuously.
- Health Validation is not a host security boundary. It does not authenticate workload submitters, enforce tenant isolation, or attest to host compliance.

### Common operator misreads

- "Exit code reflects verdict severity." Only coarsely. The binary exits 0 for PASS/INFO/WARNING, 1 for CRITICAL/UNKNOWN, and 2 when it could not run to a verdict at all (config/backend/collection/output failure). It does not distinguish CRITICAL from UNKNOWN, or INFO from WARNING — `overall_severity` in the JSON is still what callers must read for the precise level.
- "Zero skipped checks means full coverage." Skipped checks are excluded from `overall_severity`. Partial coverage from `SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL` skips does not surface in the top-level verdict. Inspect `skipped_checks`.
- "PASS means the node is healthy until the next run." The verdict is a point-in-time snapshot. Hardware state can change. Run before every job dispatch.
- "I can filter by scheduling cadence in the CLI." The CLI does not yet support cadence filtering. All registered checks run on every invocation of `health-validation run`.

### Known gaps and data accuracy notes

- **Catalog is early.** Coverage is whatever is registered in `catalog/checks.go` (CPU (count, NUMA), GPU power/state/limit/throttle, NVLink (active, fabric, fabric-health, bandwidth/count/speed, capabilities, P2P), node GPU count, InfiniBand RDMA link flap, XID, …). Do not describe coverage beyond that file. The catalog ships one InfiniBand check (`rdma_linkflap_fatal`), fed by the journal link-flap sensor; the generic IB sensor remains opt-in scaffolding (`toolkit.IBBackend`/`WithIBBackend`) for a consumer that brings additional IB backends and checks.
- **Cadence filtering is not implemented.** Checks declare `SchedulingCadence` but the CLI does not filter by it. All checks always run.
- **`health.NodeVerdict.ExitCode()` is not what the binary uses.** That catalog-library method returns a 0/1/2/3 scheme and is not called by the CLI. The binary's exit policy lives in `toolkit/app` (`exitCodeForVerdict` plus the `exitOK`/`exitUnhealthy`/`exitError` constants): PASS/INFO/WARNING→0, CRITICAL/UNKNOWN→1, tool/runtime error→2. WARNING→0 is deliberate and noted as eventually-configurable. Keep the two schemes separate.
- **OUT_OF_BAND context is defined but not used** by any registered check. The enum value exists in the codebase; no current check declares it as a valid context.
- **NVML backend is Linux-only.** The `toolkit/nvml/` package is gated on `//go:build linux` and requires cgo. The catalog and direct evaluation path work on any platform.

### Taxonomy: current checks by domain and severity

| Check ID | Category | CRITICAL triggers | WARNING triggers |
|---|---|---|---|
| `cpu_count` | cpu | Any kernel-possible logical CPU is not online | None |
| `numa_node_count` | cpu | Online NUMA node count ≠ `expected_numa_node_count` | None |
| `gpu_average_power_draw` | gpu | One-second average power draw is outside the configured range | None |
| `gpu_power_limit` | gpu | Current or enforced power limit ≠ expected (arch default or override) | None |
| `gpu_power_state` | gpu | GPU pstate ≠ `expected_gpu_pstate` | None |
| `gpu_sensor_throttle` | gpu | Any hardware/thermal/power-brake slowdown reason asserted | None |
| `gpu_count` | node | GPU count ≠ `expected_gpu_count` | None |
| `nvlink_active` | nvlink | Inactive NVLink count exceeds `max_inactive_nvlinks` | None |
| `nvlink_fabric_health` | nvlink | Any fabric-health bit asserted (degraded BW, route recovery/unhealthy, access-timeout recovery) | None |
| `nvlink_fabric` | nvlink | NVLink fabric not fully registered / fabric state unhealthy | None |
| `nvlink_bandwidth_count_speed` | nvlink | NVLink count ≠ `expected_link_count` or per-link speed ≠ `expected_speed_mbytes_per_sec` | None |
| `nvlink_capabilities` | nvlink | A required NVLink capability (P2P, sysmem access/atomics, SLI, link) is unsupported | None |
| `nvlink_p2p` | nvlink | Unhealthy P2P peer count exceeds `max_unhealthy_p2p_peers` | None |
| `rdma_linkflap_fatal` | infiniband | Count of mlx5 RDMA link outages (down→up ≥ `min_outage_seconds`) reaches `fatal_flap_threshold` | None |
| `gpu_xid_<code>` | xid | One per critical Xid code seen in the system journal (since boot) | None |
| `gpu_xid_warning` | xid | None | Consolidated warning-bucket Xid codes seen |
| `gpu_xid_154` | xid | None | None (INFO only) |

---

## Development

### Commands

```shell
# Build
bazel build //...

# Test
bazel test //...
bazel test //catalog/...          # single package
bazel test //catalog/... --test_output=streamed  # with output

# Format (gofumpt for Go, buildifier for Starlark)
bazel run //tools/format

# Check formatting without modifying
bazel test //tools/format:test

# Sync BUILD files after adding/removing Go files or imports
bazel run //tools/gazelle

# Go mod tidy — run this whenever a go.mod changes OR a new import is added.
# The repo is FOUR modules (agentkit/, catalog/, collect/, toolkit/), so tidy
# each one. //tools/go
# runs in BUILD_WORKING_DIRECTORY, so cd into the module first. CI gates on
# `mod tidy -diff` per module and fails if go.sum would drift (e.g. a
# transitive testify dep pulls in davecgh/go-spew, pmezard/go-difflib, or
# gopkg.in/yaml.v3). Commit each go.sum alongside its go.mod.
(cd agentkit && bazel run //tools/go -- mod tidy)
(cd catalog && bazel run //tools/go -- mod tidy)
(cd collect && bazel run //tools/go -- mod tidy)
(cd toolkit && bazel run //tools/go -- mod tidy)
(cd agentkit && bazel run //tools/go -- mod tidy -diff)   # verify: exit 0 + no output = CI will pass
(cd catalog && bazel run //tools/go -- mod tidy -diff)
(cd collect && bazel run //tools/go -- mod tidy -diff)
(cd toolkit && bazel run //tools/go -- mod tidy -diff)

# Run from a Linux VM or container to catch linux-only build-tag failures
# (nogo, PIE linking, etc.) that never execute on darwin.
bazel test --config=ci --platforms=//build/platforms:linux_amd64 \
  //toolkit/nvml:nvml_test

# Build the operator CLI for every supported target platform. It links NVML
# via cgo, so it is a linux-only artifact; CI builds both arches (build-only,
# no publish). On darwin the native toolchain cannot link it — use a Linux VM
# or container.
bazel build --config=ci --platforms=//build/platforms:linux_amd64 //toolkit/cmd/health-validation:health-validation
bazel build --config=ci --platforms=//build/platforms:linux_arm64 //toolkit/cmd/health-validation:health-validation

# Copy a Linux binary built directly in a Linux environment into ./bin
# (gitignored — keep local artifacts out of the repo root).
bazel build --config=ci --platforms=//build/platforms:linux_amd64 \
  //toolkit/cmd/health-validation:health-validation
mkdir -p bin
cp -L bazel-bin/toolkit/cmd/health-validation/health-validation_/health-validation ./bin/health-validation-linux-amd64

# Iterate on the platform-agnostic CLI logic locally on darwin (the NVML
# backend stubs out off-linux, so `run` can't collect, but flag parsing,
# config loading/validation, and the mock-backed tests all work). The CLI
# lives in the toolkit module, so run go from there:
(cd toolkit && go test ./cmd/health-validation)
```

The repo uses Bazel exclusively — do not use `go build` or `go test` directly for anything other than quick local iteration. See [docs/DEVELOPER.md](docs/DEVELOPER.md) for the full CI, publishing, and container workflow.

**Verifying linux-only code on darwin.** `//go:build linux` files are excluded from the host Bazel build on darwin, so local `bazel test //...` compiles them to empty stubs and nogo/link-time issues never surface. Run `bazel test --config=ci --platforms=//build/platforms:linux_amd64 <target>` from a Linux VM or container to exercise them before pushing. See [docs/DEVELOPER.md](docs/DEVELOPER.md#build-a-linux-image-from-macos) for a macOS container recipe.

### Architecture

This repo is a **hardware health check catalog and evaluation engine** for NVIDIA GPU nodes. It encodes operational judgment about GPU health conditions and produces structured verdicts consumed by automation.

#### Module layout

The repo is **four Go modules**, joined for local development by a root `go.work` (the root `go.mod` is an anchor only — it holds no packages). Only `catalog` and `toolkit` are independently versioned and published; `collect/` and `agentkit/` are in-repo only. All four are resolved together by Gazelle, so a pin in any of them can change the versions linked into the CLI:

- **`catalog/`** (`…/health-validation/catalog`) — the check catalog and evaluation engine. Self-contained: `health/` (shared vocabulary), `internal/{celeval,evaluation}/`, the `catalog` package at the module root, the per-domain check packages (`cpu/`, `gpu/`, `node/`, `nvlink/`, `xid/`), and the catalog-only codegen/dump binaries under `cmd/`. No dependency on the toolkit.
- **`toolkit/`** (`…/health-validation/toolkit`) — sensors, the NVML backend (`nvml/`), and the `health-validation` operator CLI (`cmd/health-validation/`). **Depends on the catalog module.** Resolved from `../catalog` via a `replace` in `toolkit/go.mod` until catalog is tagged and published.

The dependency direction is strict: `catalog → (nothing)`, `toolkit → catalog`. Never add a catalog import of toolkit — it would create a module cycle. (Tests are bound by the same rule: a catalog test that needs sensor-style input builds its measurement structs directly rather than importing the toolkit.) Each module is tidied and version-tagged on its own (`catalog/vX.Y.Z`, `toolkit/vX.Y.Z`).

#### Package roles

**`catalog/health/`** — shared vocabulary, no logic. Defines all domain types: `CheckDefinition`, `Rule`, `CheckVerdict`, `NodeVerdict`, the `Measurement` interface, `ErroredMeasurement`, enums (`Severity`, `Category`, etc.), and threshold governance types. No domain-specific measurement structs live here; they sit alongside their checks in `catalog/<domain>/`. Everything else imports this; it has no internal imports.

**`catalog/`** — check registry and evaluation entry point. `Catalog` is a thread-safe registry that holds check definitions and exposes `EvaluateAll(measurements...)`. Built-in checks are registered in `checks.go` via `builtinChecks()`. Per-domain subpackages hold both the typed measurement struct and the matching `CheckDefinition` factories — e.g. `catalog/node/` contains `node.NodeMeasurement` plus `node.GPUCount()`, `catalog/nvlink/` contains `nvlink.NVLinkMeasurement` plus `nvlink.NVLinkActive()`, `nvlink.NVLinkFabricHealth()` and the other NVLink checks, and `catalog/xid/` contains `xid.XidMeasurement` plus the XID check factory.

**`catalog/internal/celeval/`** — evaluates CEL expressions from check `Rule` definitions against signal values and resolved thresholds using `google/cel-go`.

**`catalog/internal/evaluation/`** — orchestrates a single check evaluation: filters by architecture/context, resolves threshold overrides, calls CEL, returns `CheckVerdict`.

**`toolkit/`** — integration layer for consumers. Defines `Sensor`, the per-domain backend interfaces, `Collect()` (runs sensors → evaluates catalog → node verdict), the `SysfsCPU` file backend, the `Journalctl` command backend, and mock helpers for testing.

#### Data flow

```
Sensor.Measure() → []Measurement
  → Catalog.EvaluateAll()
    → For each Measurement, find matching checks by Category
      → evaluation.Engine: check prerequisites, resolve thresholds, run CEL rules
        → CheckVerdict
  → AggregateNodeVerdict() → NodeVerdict
```

The binary's exit code is a coarse health signal set by `toolkit/app`: 0 for PASS/INFO/WARNING, 1 for CRITICAL/UNKNOWN, 2 when the tool could not run to a verdict (config/backend/collection/output failure). The precise level lives in `overall_severity` in the JSON. Note the catalog library's `health.NodeVerdict.ExitCode()` (0=PASS/INFO, 1=CRITICAL, 2=UNKNOWN, 3=WARNING) is a separate, unused-by-the-CLI scheme.

#### Adding a check

1. Define signals on the relevant measurement struct in `catalog/<domain>/measurement.go` (e.g. add a field + `Signal*` constant to `gpu.GPUMeasurement`).
2. Write a `CheckDefinition` factory in the same `catalog/<domain>/` package.
3. Register it in `catalog/checks.go` via `builtinChecks()`.

#### Sensors vs. backends

Two distinct concepts in `toolkit/`:

- **Sensor** — implements `toolkit.Sensor`, holds a backend, and produces `[]health.Measurement` for `Collect`. `GPUSensor` produces per-GPU measurements; `CPUSensor` produces one host-wide logical-CPU measurement.
- **Backend** — a data source for one sensor. Defined as a sensor-facing interface in `toolkit/`. `SysfsCPU` is a context-aware file backend in `toolkit/`; `NVMLBackend` lives in `toolkit/nvml/` because it is gated on linux + cgo + libnvidia-ml.

#### Adding a sensor for a new domain

1. Define the typed measurement struct (with `Category()` returning the new `health.Category` value, signal constants, a `MeasurementSignals` slice, and an `ErroredXxx` helper) in `catalog/<domain>/measurement.go`.
2. Declare the sensor-facing backend interface in `toolkit/` (e.g. `NVLinkBackend` with `Measure() ([]health.Measurement, error)`).
3. Implement `toolkit.Sensor` wrapping that interface (e.g. `NVLinkSensor`), forwarding `Measure()` from the backend.
4. Add a `MockXxx(...)` constructor alongside the real one so consumers can test without hardware — see `gpu_backend.go` for the pattern.

#### Adding a backend implementation

To plug a new data source into an existing sensor (e.g. a DCGM-based GPU backend), implement the existing backend interface (`GPUBackend.Measure`) on a struct in whichever package its dependencies require. No registration needed — Go satisfies the interface structurally. Use `NVMLBackend` (`toolkit/nvml/`) as the template for backends that need a build-tagged subpackage.

#### The operator CLI (`toolkit/cmd/health-validation`)

`toolkit/cmd/health-validation` is the operator-facing binary (`health-validation [--verbose] run [--node-id <id>] [--config <path>]`); a `version` subcommand also exists). It loads an optional YAML config, builds the catalog, runs the GPU, Node, NVLink, CPU, and Xid sensors through `toolkit.CollectWithTimeout`, and prints the `NodeVerdict` as JSON. The InfiniBand sensor is opt-in and is not wired by the stock CLI: it runs only when a consumer supplies an IB backend via `WithIBBackend`. `node_id` defaults to the hostname reported by the operating system without DNS canonicalization; `--node-id` overrides it. The config parameterizes and disables checks: each `checks.<id>.<param>` becomes a `catalog.Override` (config values are coerced to each `ThresholdSpec`'s declared Go type because CEL is type-sensitive), and each entry in the `disabled_checks` list becomes a `catalog.WithDisabledChecks` ID. Both are validated against the registered checks before the catalog is built, so a typo'd ID is an error rather than a silently-still-running check. It is the repo's first production binary and first cgo binary.
The logic lives in `toolkit/app/`; `toolkit/cmd/health-validation/main.go` is a thin shim over `app.New().Run(...)`. The backend is wired through a **platform split** so the command stays buildable and unit-testable everywhere:

- `toolkit/app/backend_linux.go` (`//go:build linux`) — `newBackend()` returns the NVML (cgo) backend.
- `toolkit/app/backend_other.go` (`//go:build !linux`) — `newBackend()` returns an "unavailable on this platform" error.
- `toolkit/app/app.go` / `toolkit/app/config.go` stay platform-agnostic; `evaluate(cat, …, sensors…)` takes sensors as arguments so tests inject `toolkit.MockGPU`/`MockNode` without hardware or cgo.

Consequence: on darwin `(cd toolkit && go build ./cmd/health-validation)` succeeds (stub backend) and `(cd toolkit && go test ./cmd/health-validation)` passes, but `health-validation run` exits `2` with the backend error — there is no data source off-linux. Follow this split for any future production binary that depends on a build-tagged backend.

When adding a direct third-party dep here (this is how `gopkg.in/yaml.v3` arrived), run `bazel run //tools/gazelle` and `(cd toolkit && bazel run //tools/go -- mod tidy)` — tidy must run inside the `toolkit` module since this binary lives there (`//tools/go` runs in `BUILD_WORKING_DIRECTORY`). Both run natively on darwin now that the `-z,lazy` linkopt is scoped to linux builds (see PIE + lazy binding below). The new external repo must also be added to `use_repo(go_deps, …)` in `third_party/go.MODULE.bazel` (gazelle's repo name for `gopkg.in/yaml.v3` is `in_gopkg_yaml_v3`, target `:yaml_v3`).

If the dep ends up linked into the CLI, `toolkit/cmd/health-validation/SHIPPED_MODULES.txt` changes too. Regenerate it with `bazel run //toolkit/cmd/health-validation:shipped_modules_update`, then run `bazel run //tools:check_notice -- --write` so `NOTICE` keeps up (without `--write` it only verifies). `//toolkit/cmd/health-validation:shipped_modules_cross_check` also checks the recorded list against the module set the Bazel graph says is linked, which catches a module that is linked but absent from the embedded list.

### Conventions

**Comments** — Keep comments sparse and concise. Explain why a non-obvious constraint or decision exists; do not narrate code, restate names, or add boilerplate. Keep required Go doc comments factual and brief, and shorten or remove redundant comments when editing nearby code.

**Commit and PR titles** — Conventional Commits (`type(scope): subject`), checked on every pull request by the `commit-lint` check. Only the PR title is checked; it is the subject that lands on `main`, and individual commit subjects are unconstrained. A leading capital is allowed (`fix: RDMA link-flap`); title-case and all-caps subjects are not. Any title whose first word is `Revert` or `revert` skips the check entirely. Rules live in `commitlint.config.js`, the type list and examples in `CONTRIBUTING.md`.

**Threshold governance** — `ThresholdCategory` controls whether operators can override a threshold: `DefaultThreshold` (overridable), `FixedThreshold` (never overridable, enforced at `OverrideSet` construction), `AdjustableThreshold` (overridable within bounds). Violations return `FixedThresholdError`.

**Enum handling** — all enums use the generic `marshalEnum[T]` / `unmarshalEnum[T]` helpers in `catalog/health/enum.go`. Follow the same pattern for new enums.

**BUILD files** — Gazelle manages `BUILD.bazel` files. After adding or removing `.go` files or changing imports, run `bazel run //tools/gazelle` to regenerate. Do not hand-edit dep lists that Gazelle owns.

**Mock constructors** — test doubles use exported constructor functions that return the interface (e.g. `MockGPU(...)`, `MockCommandExecutor(...)`), not exported mock structs. The underlying type stays unexported.

**PIE + lazy binding** — the repo sets `--@rules_go//go/config:linkmode=pie` and the hermetic LLVM toolchain hardcodes `-Wl,-z,relro,-z,now` into every link. Two adjustments keep this workable:

- `.bazel/golang.bazelrc` adds `--linkopt=-Wl,-z,lazy` under `build:ci` and `build:linux` (not globally). `-Wl,-z,now` would force `BIND_NOW` and require every dynamic symbol to resolve at load time; lld's last-wins rule for `-z now/lazy` means the trailing `-z,lazy` restores `BIND_LAZY`. `-z,relro` still applies (Partial RELRO), and cgo libraries with optional runtime deps (e.g. go-nvml expecting `libnvidia-ml.so.1` to be dlopen'd lazily) can now load on hosts that don't have those deps — critical for running the nvml tests on GPU-less CI. `-z` is an ELF-only flag that macOS's `ld64.lld` rejects, so the linkopt is **scoped to linux builds** (every linux build goes through `--config=ci`; native linux hosts pick it up via `build:linux`). Native darwin builds match neither config, so host tools like `bazel run //tools/gazelle` and `bazel run //tools/go -- mod tidy` link and run cleanly on macOS. (PIE linkmode routes even pure-Go tool binaries through the external C linker, which is why a *global* `-z,lazy` previously broke every darwin link.)
- `third_party/rules_go/0001-no-pie-in-normal-linkmode.patch` appends `-no-pie` to `extldflags` when rules_go is in `LINKMODE_NORMAL`. The hermetic clang driver defaults to `-pie`, but Go's compiler emits non-PIC object code in normal linkmode, and lld rejects the combination. This is needed for tool binaries built in normal linkmode (e.g. nogo, go_test helpers); PIE-linkmode production binaries are unaffected. The condition is on `go.mode.linkmode`, which isn't exposed to bazelrc, so it has to live in a rules_go patch.

**Struct field alignment** — nogo's `fieldalignment` analyzer gates CI. When defining a new struct (production or test), order fields largest-first to reduce padding: pointer/slice/string/map → uint64/int64 → nested structs at their real size → uint32/int32 (and named enum types built on them) → uint16/int16 → uint8/bool. **But largest-first only minimizes _padding_.** nogo *also* enforces a second metric — pointer-scan bytes (the leading span the GC must scan for pointers) — which can require a small pointer-bearing field (a `string`, pointer, or slice header) placed *before* a larger pointer-heavy nested struct, i.e. the opposite of largest-first. When two orderings tie on size, this is what nogo flags, so treat largest-first as a starting point, not a guarantee.

The standalone tool is a convenient first pass but is **not authoritative** — it tracks a newer x/tools than the pinned nogo and can accept an ordering CI rejects (a plain darwin `bazel test` has likewise passed while CI's linux nogo failed on the same struct).

**Build it with the repo's Go toolchain, or it can't analyze this code.** The tool embeds the `go/types` of whatever Go built it; a binary built with an older Go rejects these modules outright with `package requires newer Go version go1.27 (application built with go1.25)` and analyzes nothing. `go install` builds in x/tools' own module context, so it uses your default toolchain, not the repo's — pin it with `GOTOOLCHAIN` (matching the `go` directive in the module go.mod files):

```shell
GOTOOLCHAIN=go1.27.0 go install golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest
~/go/bin/fieldalignment -fix ./...
```

The CI-equivalent check runs on Linux: `bazel test --config=ci --platforms=//build/platforms:linux_amd64 <target>` — run it on the **test** target holding the struct, since a struct in a `_test.go` file is only analyzed when its `go_test` builds (an image or library build won't catch it).

For `//go:build linux`-tagged files that also pull in cgo (e.g. `toolkit/nvml/`), local checking on darwin is not reliable — darwin's clang can't cross-compile linux cgo, so nogo never runs against these files locally and the tool errors out. When CI's nogo step rejects a struct in such a file, it prints a unified-diff patch in the failure log; apply it verbatim with `patch -p1 <<'EOF' … EOF` or by hand.
