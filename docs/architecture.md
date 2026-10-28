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
| <nobr>`catalog/xid`</nobr> | `xid.XidMeasurement` struct and the Xid check factory `xid.Checks()`, which returns one CRITICAL check per critical Xid code, architecture-scoped WARNING checks, and the `gpu_xid_154` check, which reports the recovery action Xid 154 last requested for each GPU. |
| <nobr>`toolkit`</nobr> | Sensor and per-domain backend interfaces, `Collect()` / `CollectWithOptions()`, production backends including `SysfsCPU`, and mock helpers. |
| <nobr>`catalog/internal/celeval`</nobr> | Evaluates CEL expressions from check `Rule` definitions against signal values and resolved thresholds. Not a public API. |
| <nobr>`catalog/internal/evaluation`</nobr> | Orchestrates a single check evaluation: filters by architecture/context, resolves threshold overrides, calls CEL, returns `CheckVerdict`. Not a public API. |
| <nobr>`toolkit/cmd/health-validation`</nobr> | Binary entry point. Subcommands: `run`, `collect`, `version`. |

## Data flow

```
Sensor.Measure()
    --> []health.Measurement
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
- **Backend** is a data source defined as an interface in `toolkit`. The NVML-backed implementation lives in `toolkit/nvml/` (Linux only, requires cgo). `SysfsCPU` (deprecated; the CLI uses the `collect/sensors/cpu` collect sensor through the toolkit bridge) and its replacement read `/sys/devices/system/cpu/possible`, `/sys/devices/system/cpu/online`, and `/sys/devices/system/node/online` without sudo or an external executable. `Journalctl` (deprecated; the CLI uses the `collect/sensors/xid` collect sensor through the toolkit bridge) and its replacement read the current boot's kernel journal through `journalctl`. Mock backends are available for testing without hardware.

`SysfsCPU` observes the kernel CPU topology exposed in its execution environment. The `cpu_count` check compares the possible and online logical CPU counts, requires no operator value, and reports CRITICAL when they differ. It assumes the target platform does not advertise intentionally unused CPU hotplug capacity and does not validate an exact VM/SKU count, process affinity, or cgroup CPU limits; inside a container, it evaluates the sysfs view exposed by the runtime.

The `numa_node_count` check compares the online NUMA node count against `expected_numa_node_count` and reports CRITICAL when they differ. The expected value resolves from the detected architecture, and an operator override replaces whatever the architecture resolved to. Each read is attributed to its own signal, so an unreadable NUMA node list fails `numa_node_count` while `cpu_count` still evaluates, and vice versa. The check applies only to the architectures it lists and skips on any other, including a node whose architecture cannot be identified.

`health-validation collect` runs the same sensors without evaluating any checks and prints the measurements as one `ObservationBatch` (protojson). An optional config's check IDs select which sensor domains run; without one, every built-in domain runs. Use it to retain node-local evidence and evaluate it separately from acquisition. See the README for the output shape.

To add a sensor for a new hardware domain: define a measurement struct in `catalog/<domain>/`, declare a backend interface in `toolkit/`, implement a sensor wrapping it, and add a mock constructor.

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
| CheckResults | []CheckVerdict | `check_results` | Per-check results for checks that ran. |
| SkippedChecks | []CheckVerdict | `skipped_checks` | Per-check results for checks that were skipped. |
| AffectedComponents | []AffectedComponent | `affected_components` | Non-PASS domains with their affected entities and check IDs. |

`NodeVerdict` carries no node-level `RecommendedAction`. Each `CheckVerdict` carries its own remediation sequence; with several checks potentially tying at `overall_severity`, there is not a single, clearly-ranked action to surface at the node level. Consumers that need node-level remediation should walk `CheckResults`.

### CheckVerdict

| Field | Type | JSON name | Description |
|---|---|---|---|
| CheckID | string | `check_id` | Stable check identifier. |
| SpanID | string | `span_id` | UUID unique to this check invocation, run or skipped. |
| TraceID | string | `trace_id` | The run's `trace_id`, echoed here so a check result is self-correlating when consumed on its own. |
| Severity | Severity | `severity` | PASS, INFO, WARNING, CRITICAL, or UNKNOWN. |
| Category | Category | `category` | Hardware domain: cpu, gpu, node, nvlink, pcie, infiniband, storage, xid. |
| ConditionScope | ConditionScope | `condition_scope` | Blast radius of the condition: `NODE` (this node's own hardware explains it), `DOMAIN` (explaining it requires the NVLink domain shared with peer nodes), `CLUSTER` (explaining it requires the cluster network fabric beyond the node). A collection-failure verdict reports `NODE`; a skipped verdict keeps the check's declared value. Independent of `recommended_action`'s `remediation_actions`, which are always node-local. |
| Skipped | bool | `skipped` | True if the check did not run. |
| SkipReason | SkipReason | `skip_reason` | Machine-readable skip code (see below). |
| SkipDetail | string | `skip_detail` | Human-readable skip explanation. |
| AffectedEntities | []string | `affected_entities` | Device identifiers with non-PASS results (e.g. GPU UUIDs). |
| ObservedValues | map[string]any | `observed_values` | Values of the check's `RequiredSignals` (every measurement signal when it declares none). |
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
| NO_APPLICABLE_MEASUREMENT | The category was collected, but no measurement in it carries the check's required signals. Routing is by measurement schema, so a measurement that carries the signals but whose sensor failed to collect one reports CRITICAL rather than skipping. |
| SENSOR_MISSING | The check's category supplied no measurement at all — sensor absent, unconfigured, or never constructed. Registering a check declares that its sensor is expected, so every registered check reports either a result or a skip. |

## Adding a check

1. Add any new signal fields to the measurement struct in `catalog/<domain>/measurement.go`. Add matching `Signal*` string constants.
2. Write a `CheckDefinition` factory in `catalog/<domain>/`. Declare `RequiredSignals`, `Thresholds`, and `Rules` (CEL expressions referencing signal names and threshold names). Declare `Dependencies` when another check explains this one's *complete* failure to read an entity, as the GPU and NVLink checks do for `gpu_count`. A partial read, where only some required signals failed, is an independent fault and is not attributed.
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
