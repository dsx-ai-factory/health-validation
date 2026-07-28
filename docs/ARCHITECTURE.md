# NVIDIA Health Validation: Architecture

## Package roles

| Package | Role |
|---|---|
| `catalog/health` | Shared vocabulary. No logic. Defines all domain types: `CheckDefinition`, `CheckVerdict`, `NodeVerdict`, the `Measurement` interface, and all enums. No internal imports. |
| `catalog` | Check registry and evaluation entry point. `Catalog` is a thread-safe registry that holds check definitions and exposes `EvaluateAll(measurements...)`. Built-in checks are registered in `checks.go`. |
| `catalog/gpu` | `gpu.GPUMeasurement` struct consumed by GPU-category checks. |
| `catalog/node` | `node.NodeMeasurement` struct and node-level check definitions (`GPUCount`, `NVLinkActive`). |
| `catalog/nvlink` | `nvlink.NVLinkMeasurement` struct and the `NVLinkFabricHealth` check definition. |
| `catalog/xid` | `xid.XidMeasurement` struct and the XID-driven GPU check factory. |
| `toolkit` | Sensor interface, GPUBackend interface, `Collect()` function, NVML-backed GPU sensor, and mock helpers. |
| `catalog/internal/celeval` | Evaluates CEL expressions from check `Rule` definitions against signal values and resolved thresholds. Not a public API. |
| `catalog/internal/evaluation` | Orchestrates a single check evaluation: filters by architecture/context, resolves threshold overrides, calls CEL, returns `CheckVerdict`. Not a public API. |
| `toolkit/cmd/go-health-validation` | Binary entry point. Subcommands: `run`, `version`. |

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

Each sensor produces one Measurement per device. Each measurement is routed to checks whose Category matches the measurement's category. The evaluation engine resolves threshold overrides, checks architecture and execution context prerequisites, runs CEL rules, and returns a `CheckVerdict`. Aggregation computes the node-level worst-case severity. The process exit code is a coarse mapping of that severity (0 for PASS/INFO/WARNING, 1 for CRITICAL/UNKNOWN, 2 when the tool could not run to a verdict); the precise level is conveyed in the JSON.

## Sensors and backends

Two distinct concepts:

- **Sensor** implements `toolkit.Sensor`. It holds a backend and produces `[]health.Measurement` for `Collect`. Example: `GPUSensor` wraps a `GPUBackend`.
- **Backend** is a data source. Defined as an interface in `toolkit`. The NVML-backed implementation lives in `toolkit/nvml/` (Linux only, requires cgo). A mock backend is available for testing without hardware.

To add a sensor for a new hardware domain: define a measurement struct in `catalog/<domain>/`, declare a backend interface in `toolkit/`, implement a sensor wrapping it, and add a mock constructor.

## Catalog construction options

| Option | Effect |
|---|---|
| `catalog.WithArchitecture("GB200")` | Checks that don't list this architecture are skipped with `SIGNAL_UNAVAILABLE_ARCHITECTURE`. |
| `catalog.WithExecutionContext(health.OperatorInfra)` | Checks that don't list this context are skipped with `SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL`. |
| `catalog.WithDisabledChecks("check_id", ...)` | Named checks are skipped with `CHECK_DISABLED`. |
| `catalog.WithOverrides(overrides)` | Applies operator threshold overrides. Built from a config file via `Config.Overrides()`. |
| `catalog.WithCompletionThreshold(0.8)` | If fewer than 80% of checks complete, `overall_severity` is set to UNKNOWN. |

## Execution contexts

| Context | Go constant | Where it runs |
|---|---|---|
| OPERATOR_INFRA | `health.OperatorInfra` | Host OS or privileged control-plane VM. Full hardware access. |
| TENANT_GUEST | `health.TenantGuest` | Tenant VM, container, or Kubernetes worker node. No IPMI access. |
| OUT_OF_BAND | `health.OutOfBand` | BMC via Redfish. No GPU PCI visibility required. Enum value is defined but not declared by any currently registered check. |

## Output schema

### NodeVerdict

| Field | Type | JSON name | Description |
|---|---|---|---|
| Timestamp | time.Time | `timestamp` | When the evaluation completed. |
| NodeID | string | `node_id` | Opaque identifier for the node under test; the CLI defaults it to the system hostname. |
| OverallSeverity | Severity | `overall_severity` | Worst severity across all non-skipped checks. |
| SeverityByDomain | map[Category]Severity | `severity_by_domain` | Worst severity per hardware category. |
| CheckResults | []CheckVerdict | `check_results` | Per-check results for checks that ran. |
| SkippedChecks | []CheckVerdict | `skipped_checks` | Per-check results for checks that were skipped. |
| CompletionRatio | float64 | `completion_ratio` | Fraction of expected checks that completed. |
| AffectedComponents | []AffectedComponent | `affected_components` | Non-PASS domains with their affected entities and check IDs. |

### CheckVerdict

| Field | Type | JSON name | Description |
|---|---|---|---|
| CheckID | string | `check_id` | Stable check identifier. |
| Severity | Severity | `severity` | PASS, INFO, WARNING, CRITICAL, or UNKNOWN. |
| Category | Category | `category` | Hardware domain: gpu, node, nvlink, pcie, infiniband, storage. |
| Skipped | bool | `skipped` | True if the check did not run. |
| SkipReason | SkipReason | `skip_reason` | Machine-readable skip code (see below). |
| SkipDetail | string | `skip_detail` | Human-readable skip explanation. |
| AffectedEntities | []string | `affected_entities` | Device identifiers with non-PASS results (e.g. GPU UUIDs). |
| ObservedValues | map[string]any | `observed_values` | Signal values that triggered the matching rule. |
| Thresholds | map[string]ResolvedThreshold | `thresholds` | Threshold values used in evaluation, with override provenance. |
| RecommendedAction | string | `recommended_action` | Operator action string from the matching rule. |
| Messages | []string | `messages` | Human-readable verdict messages, one per matching rule. |
| MaturityLevel | string | `maturity_level` | stable or experimental. |
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
| NOT_SKIPPED | Check ran normally. |
| SIGNAL_UNAVAILABLE_ARCHITECTURE | Check does not apply to this GPU architecture. |
| SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL | Check requires a different execution context. |
| CHECK_DISABLED | Operator explicitly disabled this check. |
| DEPENDENCY_SKIPPED | A prerequisite check was skipped or failed. |
| INSUFFICIENT_HISTORY | Time-window data not yet accumulated. Applies only to checks that evaluate over a window; not used by any of the five currently registered checks. |

## Adding a check

1. Add any new signal fields to the measurement struct in `catalog/<domain>/measurement.go`. Add matching `Signal*` string constants.
2. Write a `CheckDefinition` factory in `catalog/<domain>/`. Declare `RequiredSignals`, `Thresholds`, and `Rules` (CEL expressions referencing signal names and threshold names).
3. Register it in `catalog/checks.go` via `builtinChecks()`.
4. Add a test covering the PASS path, at least one non-PASS path, and the skipped path (errored measurement).

## Build

```shell
# Canonical build (Bazel)
bazel build //toolkit/cmd/go-health-validation
bazel test //...

# Local iteration
go build -o go-health-validation ./toolkit/cmd/go-health-validation
go test ./catalog/...
```

The repo uses Bazel exclusively in CI. `go build` and `go test` work for local iteration but do not run all CI checks (nogo, link-mode validation). Use `bazel test --config=ci --platforms=//build/platforms:linux_amd64 //...` to reproduce CI results on darwin. See [DEVELOPER.md](DEVELOPER.md) for the full build, CI, and container workflow.
