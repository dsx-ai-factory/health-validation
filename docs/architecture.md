# NVIDIA Health Validation: Architecture

## Package roles

| Package | Role |
|---|---|
| <nobr>`catalog/health`</nobr> | Shared vocabulary and node-verdict aggregation. Defines all domain types: `CheckDefinition`, `CheckVerdict`, `NodeVerdict`, the `Measurement` interface, and all enums. It depends on the module's leaf `internal/version` package for verdict provenance and does not import check-domain or evaluation packages. |
| <nobr>`catalog`</nobr> | Check registry and evaluation entry point. `Catalog` is a thread-safe registry that holds check definitions and exposes `EvaluateAll(measurements...)` plus per-run configuration through `EvaluateAllWithOptions`. Built-in checks are registered in `checks.go`. |
| <nobr>`catalog/cpu`</nobr> | `cpu.CPUMeasurement` and the host-wide `CPUCount` and `NUMANodeCount` check definitions. |
| <nobr>`catalog/gpu`</nobr> | `gpu.GPUMeasurement` struct consumed by GPU-category checks, plus the `GPUPowerLimit` and `GPUPowerState` check definitions. |
| <nobr>`catalog/node`</nobr> | `node.NodeMeasurement` struct and the node-level `GPUCount` check definition. |
| <nobr>`catalog/nvlink`</nobr> | `nvlink.NVLinkMeasurement` struct and its NVLink check definitions (`NVLinkActive`, `NVLinkFabricHealth`, `NVLinkFabric`, `NVLinkBandwidthCountSpeed`, `NVLinkCapabilities`, `NVLinkP2P`). |
| <nobr>`catalog/infiniband`</nobr> | `infiniband.LinkFlapMeasurement` struct and the `RDMALinkFlapFatal` check definition (category `infiniband`). |
| <nobr>`catalog/xid`</nobr> | `xid.XidMeasurement` struct and the Xid check factory `xid.Checks()`, which returns one CRITICAL check per critical Xid code, architecture-scoped WARNING checks, the INFO `gpu_xid_154` check, `gpu_xid_informational`, which reports the Xid codes NVIDIA's catalog gives no resolution, and `gpu_xid_unknown`, which reports Xid codes no registered check covers. |
| <nobr>`toolkit`</nobr> | Sensor and per-domain backend interfaces, `Collect()` / `CollectWithOptions()`, production backends including `SysfsCPU`, and mock helpers. |
| <nobr>`catalog/internal/celeval`</nobr> | Evaluates CEL expressions from check `Rule` definitions against signal values and resolved thresholds. Not a public API. |
| <nobr>`catalog/internal/evaluation`</nobr> | Orchestrates a single check evaluation: filters by architecture/context, resolves threshold overrides, calls CEL, returns `CheckVerdict`. Not a public API. |
| <nobr>`toolkit/cmd/health-validation`</nobr> | Binary entry point. Subcommands: `run`, `collect`, `version`. |

## Data flow

```
collect.Sensor.Measure()                  toolkit.Sensor.Measure()
    --> ObservationBatch                      (legacy backends)
        --> collect adapter                       |
            --> []health.Measurement <------------+
        --> Catalog.EvaluateAll()
            --> per-category check routing
                --> CEL evaluation per check
                    --> []CheckVerdict
                        --> AggregateNodeVerdict()
                            --> NodeVerdict
```

Each sensor produces one or more measurements. Device sensors normally emit one per device; the CPU sensor emits one host-wide measurement. Each measurement is routed to the checks whose `Category` matches the measurement's category. The evaluation engine resolves threshold overrides, checks architecture and execution context prerequisites, runs CEL rules, and returns a `CheckVerdict`. Aggregation computes the node-level worst-case severity. The process exit code is a coarse mapping of that severity (0 for PASS/INFO/WARNING, 1 for CRITICAL/UNKNOWN, 2 when the tool could not run to a verdict); the precise level is conveyed in the JSON.

## Sensors and backends

Two distinct concepts:

- **Sensor** implements `toolkit.Sensor`. It holds a backend and produces `[]health.Measurement` for `Collect`. For example, `GPUSensor` wraps a `GPUBackend`, and `CPUSensor` wraps a `CPUBackend`.
- **Backend** is a data source defined as an interface in `toolkit`. The NVML-backed implementation lives in `toolkit/nvml/` (Linux only, requires cgo). `SysfsCPU` (deprecated; the CLI uses the `collect/sensors/cpu` collect sensor through the toolkit bridge) and its replacement read `/sys/devices/system/cpu/possible`, `/sys/devices/system/cpu/online`, and `/sys/devices/system/node/online` without sudo or an external executable. `Journalctl` and `JournalctlRDMALinkFlap` (deprecated; the CLI uses the `collect/sensors/xid` and `collect/sensors/linkflap` collect sensors through the toolkit bridge) and their replacements read the current boot's kernel journal through `journalctl`. The link-flap sensor reads only the lookback window (default one hour) and reports each mlx5 link transition keyed by its journal entry. It reads a line as a transition only when the `mlx5_core` driver prefix starts the message, and checks the interface it names against the kernel's naming rules rather than assuming it is valid. The shared `infiniband` adapter pairs each interface's downs with their next ups into one node-wide `infiniband.LinkFlapMeasurement`. Only outages whose down and up both fall in the window count: a link still down at scan time, or one that went down before the window, records nothing, and a transition logged under an interface's pre-rename name does not pair with its later name. A transition line the parser cannot read fails `rdma_linkflap_fatal` closed rather than being skipped. Mock backends are available for testing without hardware.

`SysfsCPU` observes the kernel CPU topology exposed in its execution environment. The `cpu_count` check compares the possible and online logical CPU counts, requires no operator value, and reports CRITICAL when they differ. It assumes the target platform does not advertise intentionally unused CPU hotplug capacity and does not validate an exact VM/SKU count, process affinity, or cgroup CPU limits; inside a container, it evaluates the sysfs view exposed by the runtime.

The `numa_node_count` check compares the online NUMA node count against `expected_numa_node_count` and reports CRITICAL when they differ. The expected value resolves from the detected architecture, and an operator override replaces whatever the architecture resolved to. Each read is attributed to its own signal, so an unreadable NUMA node list fails `numa_node_count` while `cpu_count` still evaluates, and vice versa. The check applies only to the architectures it lists and skips on any other, including a node whose architecture cannot be identified.

`health-validation collect` runs the same sensors without evaluating any checks and prints the measurements as one `ObservationBatch` (protojson). An optional config's check IDs select which sensor domains run; without one, every built-in domain runs. Use it to retain node-local evidence and evaluate it separately from acquisition. See the README for the output shape.

To add a sensor for a new hardware domain: define a measurement struct in `catalog/<domain>/`, declare a backend interface in `toolkit/`, implement a sensor wrapping it, and add a mock constructor.

## Collect bridge

The CLI collects through `collect` sensors. `toolkit/internal/collectbridge` registers each source once per run, measures every source once under one collection cycle, and caches the observations; the adapters in `toolkit/internal/collectadapters` and `toolkit/observations` turn them into catalog measurements. Each source has its own timeout budget. A source that times out or fails as a whole is logged once as a warning (errors on individual GPUs are not logged) and reported in the verdict as a collection failure on the checks it feeds; checks fed by other sources still evaluate. The native NVML and DCGM sources refuse a new call while a timed-out call in the same process is still running.

The bridge is the production adapter layer between `collect` and the catalog. Extracting its runner into a shared coordinator waits until a second caller, such as `agentkit/server`, runs the same runner.

## Catalog construction options

| Option | Effect |
|---|---|
| <nobr>`catalog.WithArchitecture("GB200")`</nobr> | Checks that don't list this architecture are skipped with `SIGNAL_UNAVAILABLE_ARCHITECTURE`. |
| <nobr>`catalog.WithDriverVersion(v)`</nobr> | Records the host's NVIDIA driver version (a `health.DriverVersion`) for derived signals that depend on it. Leave it unset when the version is unknown. |
| <nobr>`catalog.WithExecutionContext(health.OperatorInfra)`</nobr> | Checks that don't list this context are skipped with `SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL`. |
| <nobr>`catalog.WithDisabledChecks("check_id", ...)`</nobr> | Named checks are skipped with `CHECK_DISABLED`. Built from a config file's `disabled_checks:` via `Config.DisabledCheckIDs()`. |
| <nobr>`catalog.WithOverrides(overrides)`</nobr> | Applies operator threshold overrides. Built from a config file via `Config.Overrides()`. |

Scheduling cadence is not a construction option. Set `Cadence` in per-run `catalog.EvaluationOptions` or `toolkit.CollectionOptions`; either zero value evaluates every registered check, and zero-value collection options also use the default timeout. Checks outside the requested window are reported with `CADENCE_MISMATCH`. One catalog can serve Continuous, Prolog, and Maintenance windows.

`health-validation run` defaults to `TENANT_GUEST`; `--execution-context`
selects another context. The CLI applies the selected context through
`catalog.WithExecutionContext` when constructing the catalog.

## Execution contexts

| Context | Go constant | Where it runs |
|---|---|---|
| OPERATOR_INFRA | `health.OperatorInfra` | Host OS or privileged control-plane VM. Full hardware access. |
| TENANT_GUEST | `health.TenantGuest` | Tenant VM, container, or Kubernetes worker node. No IPMI access. |
| OUT_OF_BAND | `health.OutOfBand` | BMC via Redfish. No GPU PCI visibility required. Enum value is defined but not declared by any currently registered check. |

## Output schema

The `NodeVerdict` JSON is a versioned contract. Its `schema_version` field uses MAJOR.MINOR versioning (`health.NodeVerdictSchemaVersion` in `catalog/health/verdict.go`, mirrored by `catalog/health/node-verdict.schema.json`); MAJOR increments on breaking changes and MINOR on additive ones. Consumers must check this field before acting on a verdict and treat an unrecognized or missing value as untrusted.

### NodeVerdict

| Field | Type | JSON name | Description |
|---|---|---|---|
| SchemaVersion | string | `schema_version` | JSON schema version of this verdict (MAJOR.MINOR); always present. Consumers must check it before acting. |
| CatalogVersion | string | `catalog_version` | Catalog module version that produced this verdict; stamped builds use a release or pseudo-version, while unstamped local builds use the module version Go embeds, `dev+<commit>`, or `dev`. |
| Timestamp | time.Time | `timestamp` | When the evaluation completed. |
| NodeID | string | `node_id` | Opaque identifier for the node under test; the CLI defaults it to the system hostname. |
| TraceID | string | `trace_id` | UUID identifying this run; echoed on every `CheckVerdict` it produced. |
| OverallSeverity | Severity | `overall_severity` | Worst severity across all non-skipped checks. |
| SeverityByDomain | map[Category]Severity | `severity_by_domain` | Worst severity per hardware category. |
| RecommendedAction | RecommendedAction | `recommended_action` | Node-level drain sequence selected from the highest-severity eligible failing check; elided (`omitzero`) when no node-level drain sequence is available. |
| CheckResults | []CheckVerdict | `check_results` | Per-check results for checks that ran. |
| SkippedChecks | []CheckVerdict | `skipped_checks` | Per-check results for checks that were skipped. |
| AffectedComponents | []AffectedComponent | `affected_components` | Non-PASS domains with their affected entities and check IDs. |

Each `CheckVerdict` retains its own recommendation; drain recommendations carry
an ordered remediation sequence. `NodeVerdict` also surfaces one complete drain
sequence, selected from eligible failing checks by severity, then each step's
documented rank in sequence order, then check ID. Action names provide a final
stability fallback for repeated check IDs. Consumers track which steps they
have attempted and re-run the suite after each step.

Severity is ordered PASS < INFO < WARNING < UNKNOWN < CRITICAL.

### CheckVerdict

| Field | Type | JSON name | Description |
|---|---|---|---|
| CheckID | string | `check_id` | Stable check identifier. |
| SpanID | string | `span_id` | UUID unique to this check invocation, run or skipped. |
| TraceID | string | `trace_id` | The run's `trace_id`, echoed here so a check result is self-correlating when consumed on its own. |
| Severity | Severity | `severity` | PASS, INFO, WARNING, UNKNOWN, or CRITICAL. |
| Category | Category | `category` | Hardware domain: cpu, gpu, node, nvlink, pcie, infiniband, storage, xid. |
| ConditionScope | ConditionScope | `condition_scope` | Blast radius of the condition: `NODE` (this node's own hardware explains it), `DOMAIN` (explaining it requires the NVLink domain shared with peer nodes), `CLUSTER` (explaining it requires the cluster network fabric beyond the node). A collection-failure verdict reports `NODE`; a skipped verdict keeps the check's declared value. Independent of `recommended_action`'s `remediation_actions`, which are always node-local. |
| Skipped | bool | `skipped` | True if the check did not run. |
| SkipReason | SkipReason | `skip_reason` | Machine-readable skip code (see below). |
| SkipDetail | string | `skip_detail` | Human-readable skip explanation. |
| AffectedEntities | []string | `affected_entities` | Device identifiers with non-PASS results (e.g. GPU UUIDs). |
| ObservedValues | map[string]any | `observed_values` | Values of the check's `RequiredSignals`. A measurement that covers more than one check narrows them to the check's own part: an Xid check reports only its own code's events and counts. |
| Thresholds | map[string]ResolvedThreshold | `thresholds` | Threshold values used in evaluation, with override provenance. |
| RecommendedAction | RecommendedAction | `recommended_action` | Structured remediation object `{operational_state, remediation_actions}`. `remediation_actions` is an ordered sequence of remediation steps for a remediation engine to work through, present only when `operational_state` is `drain`; the field is elided (`omitzero`) when no action applies. |
| Messages | []string | `messages` | Human-readable verdict messages, one per matching rule. |
| MaturityLevel | enum | `maturity_level` | `stable` or `experimental`. |
| NVIDIAEndorsed | bool | `nvidia_endorsed` | True for `provenance: nvidia` checks. |
| Timestamp | time.Time | `timestamp` | When this check was evaluated. |

### ResolvedThreshold

| Field | JSON name | Description |
|---|---|---|
| Value | `value` | Effective value used in evaluation (may be operator-overridden). |
| OriginalValue | `original_value` | Catalog-authored default. |
| Category | `category` | DEFAULT, FIXED, or ADJUSTABLE. |
| Overridden | `overridden` | True if an operator override was applied. |

## Skip reason codes

| Code | Meaning |
|---|---|
| NOT_SKIPPED | The check ran normally. |
| SIGNAL_UNAVAILABLE_ARCHITECTURE | The check does not apply to this GPU architecture. |
| SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL | The check requires a different execution context. |
| CHECK_DISABLED | An operator disabled this check. |
| INSUFFICIENT_HISTORY | Time-window data has not yet accumulated. Applies only to checks that evaluate over a window; no currently registered check uses it. |
| CADENCE_MISMATCH | The check is not scheduled for the requested `--cadence` window. |
| THRESHOLD_UNCONFIGURED | A threshold the operator must supply was not configured. |
| NO_APPLICABLE_MEASUREMENT | The category was collected, but no measurement in it carries the check's required signals. Routing is by measurement schema, so an applicable check fed by a measurement that carries the required signals but reports a collection error for one produces UNKNOWN rather than skipping. |
| SENSOR_MISSING | The check's category supplied no measurement at all — sensor absent, unconfigured, or never constructed. Registering a check declares that its sensor is expected, so every registered check reports either a result or a skip. |

See [Empty results and collection failures](#empty-results-and-collection-failures) for how these differ from a collection failure.

## Empty results and collection failures

Every check that runs ends in one of three outcomes:

- **Value.** Its required signals were collected, so its rules ran and `observed_values` is set.
- **Collection failure.** A required signal could not be read, so the rules did not run. `observed_values` is absent, every message starts with `measurement unavailable: `, and the action is `check_collection`, or `retry_collection` after a timeout.
- **Skip.** The check did not apply, or its data never arrived.

A collection failure is never a PASS and never exits 0. Identify one by its message prefix and action rather than its severity, which is UNKNOWN today.

Skips never count toward `overall_severity`, so read `skipped_checks`. `SENSOR_MISSING` and `NO_APPLICABLE_MEASUREMENT` mean data the run expected never arrived; the other reasons are deliberate exclusions.

| Case | Evidence of collection | Outcome | Exit |
|---|---|---|---|
| A check names no `RequiredSignals`, or a blank one | None | Registration fails; the CLI prints no verdict. `run` registers checks after GPU architecture discovery, so if discovery fails it reports that verdict instead | 2, or 1 if discovery fails |
| Every required signal was collected | Values | Value | 0 or 1 by severity |
| A required signal failed, alone or with others | Collection error on that signal | Collection failure | 1 |
| Only signals the check does not require failed | Values for the required signals | Value | 0 or 1 by severity |
| A per-GPU measurement carries values but no verified identity | Values under an unidentified, slot, or source identity | No verdict: ambiguous measurement error | 2 |
| A GPU measurement with no verified identity reports a required-signal failure | Collection error | Collection failure | 1 |
| GPU inventory succeeds and finds no GPUs | Inventory read with zero devices | The CLI stops at architecture discovery: CRITICAL `architecture_discovery` with a drain | 1 |
| GPU inventory read fails or times out | Inventory collection error | The same discovery verdict in the CLI; a library caller sees a `gpu_count` collection failure | 1 |
| Some GPU handles cannot be opened | Inventory with failed slots | Those GPUs are not counted, so `gpu_count` judges the lower count. If no remaining GPU model can be read, the CLI stops at architecture discovery instead | 0 or 1 by severity |
| The Xid journal scan succeeds with no Xid entries | Scan marker | Value: zero events, so the Xid checks pass | 0 or 1 by the other checks |
| The kernel journal is unreadable or holds no kernel entries (Xid) | Read error, or an empty kernel-scoped probe | Collection failure for every Xid check | 1 |
| The RDMA link-flap scan finds no link transitions | Any entry in the current boot's journal | Value: zero flaps, so `rdma_linkflap_fatal` passes | 0 or 1 by the other checks |
| An Xid line matches but does not parse | Parse error on the event | Collection failure for every Xid check | 1 |
| A CPU sysfs file cannot be read | Collection error on that signal | Collection failure for the checks that read it | 1 |
| A built-in domain adapter receives no observations | Empty batch from every sensor that feeds it | Collection failure for every signal of the domain | 1 |
| A source reports `NOT_SUPPORTED` for a hardware-slowdown bit or an NVLink fabric-health bit | `NoValue` | Read as not asserted; the check evaluates | 0 or 1 by severity |
| A source reports `NOT_SUPPORTED` for the Fabric Manager status | `NoValue` | Read as fabric registration incomplete, so `nvlink_fabric` fails on its rule | 1 |
| Any other `NoValue`, including `NO_TARGETS`, or a collection error in the `UNSUPPORTED` category, except the NVLink and compute-process readings listed below | `NoValue` or collection error | Collection failure | 1 |
| A library caller supplies no measurement for a registered check's category | None | Skip: `SENSOR_MISSING` | Not counted |
| A category is collected but no measurement carries the check's signals | Other measurement shapes | Skip: `NO_APPLICABLE_MEASUREMENT` | Not counted |
| Disabled, wrong architecture, execution context or cadence, or an unconfigured required threshold | Not collected by design | Skip with that reason | Not counted |

Differences between domains:

- The Xid reader proves a clean scan with a scan marker and a kernel-scoped probe. An empty result alone is not a clean scan. The RDMA link-flap reader still uses the legacy `journalctl` backend, whose probe accepts any entry in the current boot's journal, not only kernel messages.
- GPU count proves zero from the inventory read itself. `NO_TARGETS` on a per-GPU signal never proves a node has no GPUs.
- When a source says `NOT_SUPPORTED`, seven signals read as not asserted: the three hardware-slowdown bits and the four NVLink fabric-health bits (`unsupportedIsNeutral` in `toolkit/observations/mapping.go`). The Fabric Manager status reads as registration incomplete (`unsupportedValue`).
- The NVLink converter (`toolkit/observations/nvlink.go`) follows the legacy NVML reads. A link state NVML reports as `ERROR_NOT_SUPPORTED` is not counted as inactive, and reported for the whole GPU it means the GPU has no NVLink. An unsupported common speed falls back to the slowest per-link speed. `NO_TARGETS` on the P2P status means a GPU with no peers.
- `NO_TARGETS` on the compute-process cmdline signal means no compute processes; with a successfully collected process count of zero, `gpu_compute_apps` passes.
- Every other unsupported value fails the checks that need it.
- The library converter `observations.ToMeasurements` returns no measurement for an empty input, which evaluation reports as `SENSOR_MISSING`. The CLI's built-in adapters fail the whole domain instead, so treat an empty conversion as missing data.
- In the stock CLI, GPU telemetry comes from DCGM through the collect GPU adapter, except compute processes, which come from NVML. The rules above apply; DCGM reports an unsupported slowdown bit as `NOT_SUPPORTED`. NVLink comes through the collect NVLink adapter: the Fabric Manager status from DCGM, and links, P2P, and the four fabric-health bits from NVML, which reports an unsupported fabric-health bit as `NOT_SUPPORTED`. A GPU with no NVLink reads zero inactive links, so `nvlink_active` passes. When both checks apply, `nvlink_bandwidth_count_speed` fails; on an architecture it does not cover, such as A100, it skips instead.

## Adding a check

1. Add any new signal fields to the measurement struct in `catalog/<domain>/measurement.go`. Add matching `Signal*` string constants.
2. Write a `CheckDefinition` factory in `catalog/<domain>/`. Declare `RequiredSignals` (at least one; registration rejects an empty list or a blank name), `Thresholds`, and `Rules` (CEL expressions referencing signal names and threshold names). Declare `Dependencies` when another check explains this one's *complete* failure to read an entity, as the GPU and NVLink checks do for `gpu_count`. A partial read, where only some required signals failed, is an independent fault and is not attributed.
3. Register it in `catalog/checks.go` via `builtinChecks()` so `catalog.New()` and the stock CLI run it. Declare `SchedulingCadence` on the check. Omit `--cadence` (or use a zero-value evaluation/collection context) to evaluate every registered check; set a cadence to run checks whose cadence covers that window (Continuous ⊂ Prolog ⊂ Maintenance) and report the others as skipped.
4. Add a test covering the PASS path, at least one non-PASS path, and the skipped path (errored measurement).

## Build

```shell
# Canonical build (Bazel)
bazel build //toolkit/cmd/health-validation
bazel test //...

# Local iteration
go build -o health-validation ./toolkit/cmd/health-validation
go test ./catalog/...
```

The repo uses Bazel exclusively in CI. `go build` and `go test` work for local iteration but do not run all CI checks (nogo, link-mode validation). On macOS, run `bazel test --config=ci --platforms=//build/platforms:linux_amd64 //...` from a Linux VM or container to reproduce the Linux CI build. See [Developer Guide on GitHub](https://github.com/dsx-ai-factory/health-validation/blob/main/docs/developer.md#build-a-linux-image-from-macos) for the container workflow.
