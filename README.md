# NVIDIA Health Validation

[![CI](https://github.com/dsx-ai-factory/health-validation/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/dsx-ai-factory/health-validation/actions/workflows/ci.yml)

NVIDIA Health Validation runs on a GPU node, evaluates hardware and driver state against calibrated thresholds, and returns a structured verdict. The verdict answers one question: is this node ready to accept the next workload?

The repository is `github.com/dsx-ai-factory/health-validation`. It is a single Go module, released under one version tag, whose main packages are:

- **`catalog`** (`…/health-validation/catalog`): Check definitions, threshold logic, severity classifications, and a CEL-based evaluation engine. No hardware dependency. Import this if you already collect GPU telemetry and want NVIDIA's operational judgment applied to your signal stream.
- **`toolkit`** (`…/health-validation/toolkit`): Sensor implementations (including NVML-backed GPU collection and CPU collection from Linux sysfs), mock backends for testing, and a `Collect()` function that runs sensors, evaluates all checks, and returns a node-level verdict. Import this if you want the full working sensor and evaluation stack.

`toolkit` depends on `catalog`; import `toolkit` to get both.

For raw observations without catalog evaluation, use the experimental `collect`
package. See [Shared raw collection library](docs/integration.md#shared-raw-collection-library)
for its ten collection families, typed targets, and custom sensor registration.

> **Stability.** The durable surface is the **catalog** — the check definitions and the evaluation engine. The `toolkit`'s sensor/backend layer that feeds it is a reference implementation and is subject to change.

## What it does

The check engine collects typed measurements from sensors, evaluates them against catalog-defined CEL rules and thresholds, and aggregates the results into a node-level verdict. Each check produces a per-check result. The node-level verdict is the worst severity across all checks that ran.

### Example: clean node

```json
{
  "schema_version": "0.3",
  "catalog_version": "v0.1.0",
  "timestamp": "2026-06-05T14:32:01Z",
  "trace_id": "6f1f6f2c-2a70-4d3e-9d1a-0b8f1a3c5d21",
  "overall_severity": "PASS",
  "severity_by_domain": {},
  "check_results": [
    {
      "timestamp": "2026-06-05T14:32:01Z",
      "check_id": "gpu_count",
      "span_id": "b4c7e0d9-59a1-4f0e-8c2b-1d6e7a9f3042",
      "trace_id": "6f1f6f2c-2a70-4d3e-9d1a-0b8f1a3c5d21",
      "severity": "PASS",
      "category": "node",
      "condition_scope": "NODE",
      "skipped": false,
      "skip_reason": "NOT_SKIPPED",
      "maturity_level": "stable",
      "nvidia_endorsed": true
    }
  ],
  "skipped_checks": []
}
```

### Example: node with an Xid and GPU condition

```json
{
  "schema_version": "0.3",
  "catalog_version": "v0.1.0",
  "timestamp": "2026-06-10T14:31:12.337336529-05:00",
  "trace_id": "0c9a4b83-7e5d-4a11-93f6-2c8b5e1470af",
  "severity_by_domain": {
    "node": "CRITICAL",
    "xid": "CRITICAL"
  },
  "affected_components": [
    {
      "check_ids": [
        "gpu_count"
      ],
      "category": "node",
      "max_severity": "CRITICAL"
    },
    {
      "check_ids": [
        "gpu_xid_79"
      ],
      "category": "xid",
      "max_severity": "CRITICAL"
    }
  ],
  "check_results": [
    {
      "timestamp": "2026-06-10T14:31:12.330483625-05:00",
      "observed_values": {
        "gpu_count": 1
      },
      "thresholds": {
        "expected_gpu_count": {
          "value": 4,
          "original_value": 4,
          "category": "DEFAULT",
          "overridden": false
        }
      },
      "maturity_level": "stable",
      "check_id": "gpu_count",
      "span_id": "1a2f5c07-3b64-4e8d-9a70-5f1c2d8e46b3",
      "trace_id": "0c9a4b83-7e5d-4a11-93f6-2c8b5e1470af",
      "recommended_action": {
        "remediation_actions": ["reset_gpu", "restart_os", "service_required"],
        "operational_state": "drain"
      },
      "messages": [
        "Expected 4 GPUs, found 1."
      ],
      "severity": "CRITICAL",
      "category": "node",
      "condition_scope": "NODE",
      "skipped": false,
      "nvidia_endorsed": true
    },
    {
      "timestamp": "2026-06-10T14:31:12.331524621-05:00",
      "observed_values": {
        "gpu_id": "GPU-5f1c2d8e-46b3-4e8d-9a70-1a2f5c073b64",
        "inactive_count": 0
      },
      "thresholds": {
        "max_inactive_nvlinks": {
          "value": 0,
          "original_value": 0,
          "category": "DEFAULT",
          "overridden": false
        }
      },
      "maturity_level": "stable",
      "check_id": "nvlink_active",
      "span_id": "8d3b6e42-97ac-4d51-b0e8-3a7f4c15928d",
      "trace_id": "0c9a4b83-7e5d-4a11-93f6-2c8b5e1470af",
      "severity": "PASS",
      "category": "nvlink",
      "condition_scope": "NODE",
      "skipped": false,
      "nvidia_endorsed": true
    },
    {
      "timestamp": "2026-06-10T14:31:12.332512987-05:00",
      "observed_values": {
        "xid_counts": {
          "79": 1
        }
      },
      "maturity_level": "stable",
      "check_id": "gpu_xid_79",
      "span_id": "e57c190b-46d2-4c8a-a3f1-9b04d7e2635c",
      "trace_id": "0c9a4b83-7e5d-4a11-93f6-2c8b5e1470af",
      "recommended_action": {
        "remediation_actions": ["reset_gpu", "service_required"],
        "operational_state": "drain"
      },
      "messages": [
        "GPU Xid 79 detected: GPU has fallen off the bus."
      ],
      "severity": "CRITICAL",
      "category": "xid",
      "condition_scope": "NODE",
      "skipped": false,
      "nvidia_endorsed": true
    }
  ],
  "skipped_checks": [],
  "overall_severity": "CRITICAL"
}
```

The process exit code is a coarse health signal: `0` for PASS/INFO/WARNING, `1` for CRITICAL/UNKNOWN, and `2` when the tool could not run to a verdict (config/backend/collection/output failure). It does not distinguish CRITICAL from UNKNOWN or INFO from WARNING, and it does not reflect `skipped_checks` — read `overall_severity` in the JSON for the precise level.

The verdict JSON is a versioned contract. [`catalog/health/node-verdict.schema.json`](catalog/health/node-verdict.schema.json) is the JSON Schema for it (`schema_version` MAJOR.MINOR); validate output against it in downstream consumers. `catalog_version` names the module version, and so the catalog, that produced the verdict, so consumers can verify which check logic and default thresholds were available when the run occurred.

## How it works

```
Sensor.Measure() --> []Measurement --> Catalog.EvaluateAll() --> NodeVerdict
```

Sensors collect typed measurements from hardware. A sensor can produce one measurement per device or one host-wide measurement. The toolkit includes NVML-backed GPU, node, and NVLink sensors, a CPU sensor reading Linux sysfs (the CLI collects CPU through `collect/sensors/cpu`), an Xid sensor backed by `journalctl`, and mock constructors for testing. Sensors that cannot observe their source return an errored measurement rather than failing the whole run; the catalog decides per-check whether to surface a CRITICAL verdict or skip.

The CPU sensor counts the logical CPUs in `/sys/devices/system/cpu/possible` and `/sys/devices/system/cpu/online`, and the NUMA nodes in `/sys/devices/system/node/online`. The `cpu_count` check requires no operator configuration and reports CRITICAL when a kernel-possible CPU is not online. It assumes the target platform does not advertise intentionally unused CPU hotplug capacity and does not validate an exact VM or machine-SKU count, process affinity, or cgroup CPU limits.

The `numa_node_count` check compares the online NUMA node count against `expected_numa_node_count` and reports CRITICAL when they differ — typically a platform firmware setting, a kernel boot argument, or a node provisioned as the wrong SKU. The expected value resolves from the detected architecture, and operators override it per fleet. The check lists the architectures it applies to and skips on any other, including a node whose architecture cannot be identified.

Catalog receives measurements, routes each to matching checks by category (GPU measurements run GPU checks, node measurements run node checks), evaluates each check's CEL rules against the measurement values and resolved thresholds, and produces a flat list of check verdicts.

Aggregation takes the flat list and computes the node-level verdict: the worst severity across all non-skipped checks, a `severity_by_domain` map, and a list of affected components for non-PASS results.

Per-check results use five severity levels:

| Severity | Meaning |
|---|---|
| PASS | No condition detected. Node is eligible for the next workload. |
| INFO | Anomaly within safe operating bounds. Monitor for trend. |
| WARNING | Condition that degrades performance or increases failure risk. |
| CRITICAL | Imminent risk of failure, data corruption, or unrecoverable state. Remove from service. |
| UNKNOWN | Check could not complete. Eligibility for the affected component cannot be assessed. |

`skipped` is a separate per-check state, not a severity level. A skipped check contributes nothing to the node-level verdict.

## Check catalog

The catalog includes these check families:

| Check ID | Category | What it evaluates | Configurable thresholds |
|---|---|---|---|
| `cpu_count` | cpu | Whether every kernel-possible logical CPU is online | None |
| `numa_node_count` | cpu | Whether the node brought online the expected number of NUMA nodes | `expected_numa_node_count` |
| `gpu_average_power_draw` | gpu | Whether each GPU's one-second average power draw is within the configured range | `min_power_w`, `max_power_w` |
| `gpu_power_limit` | gpu | Whether each GPU's power limit matches the expected value | `expected_power_limit_w` |
| `gpu_power_state` | gpu | Whether each GPU is in the expected power state | `expected_gpu_pstate` |
| `gpu_sensor_throttle` | gpu | Whether any GPU is being clamped by hardware slowdown, thermal, or power-brake protection | None |
| `gpu_compute_apps` | gpu | Whether any compute process still holds a GPU | None |
| `gpu_count` | node | Number of GPUs present on the node | `expected_gpu_count` |
| `nvlink_active` | nvlink | Number of active NVLink connections | `max_inactive_nvlinks` |
| `nvlink_fabric_health` | nvlink | NVLink fabric-health fault bits (degraded bandwidth, route recovery, access timeout) | None |
| `nvlink_fabric` | nvlink | Whether each GPU is registered with the NVLink fabric | None |
| `nvlink_bandwidth_count_speed` | nvlink | Per-GPU NVLink count and per-link speed | `expected_link_count`, `expected_speed_mbytes_per_sec` |
| `nvlink_capabilities` | nvlink | Whether each GPU reports its expected NVLink capabilities | None |
| `nvlink_p2p` | nvlink | Peer GPUs reachable over NVLink P2P | `max_unhealthy_p2p_peers` |
| `rdma_linkflap_fatal` | infiniband | mlx5 RDMA link outages that stayed down past a window | `min_outage_seconds`, `fatal_flap_threshold` |
| `gpu_xid_<code>` | xid | One check per Xid code seen in the journal, with catalog-defined severity and action | `occurrences`; Xids 144–150 use `fatal_occurrences`, `nonfatal_occurrences` |
| `gpu_xid_149_<hex>` | xid | One check per Xid 149 NETIR_LINK_EVT/NETIR_LINK_DOWN subtype NVIDIA's decode table distinguishes | `occurrences` |
| `gpu_xid_154` | xid | Xid 154 (INFO — recovery action changed) | `occurrences` |

Each check definition carries:

- `execution_contexts`: where the check can run (OPERATOR_INFRA, TENANT_GUEST)
- `category`: the hardware domain (cpu, gpu, node, nvlink, pcie, infiniband, storage, xid)
- `provenance`: `nvidia` for all built-in checks
- `maturity_level`: stable or experimental
- `condition_scope`: how far the condition reaches (NODE, DOMAIN, CLUSTER)

> **GB300 support.** Most checks in the catalog declare support for both GB200 and GB300 in their `Architectures` field. GB200 support has been validated; GB300 support has not yet completed the same validation pass. Treat GB300 results as directionally correct but experimental and subject to change until this note is removed.

Xid checks use the public NVIDIA Xid catalog's per-code applicability. H200
uses the H100 applicability matrix because both products use the Hopper
architecture; Xid 144-150 and their subtype checks remain Blackwell-only.
Warning-class Xids are individual `gpu_xid_<code>` checks.

## Prerequisites

- go 1.27.3
- Linux sysfs exposing `/sys/devices/system/cpu/{possible,online}` and `/sys/devices/system/node/online` (required for CPU collection)
- NVIDIA driver (required for the GPU sensor; not required if you supply measurements directly)
- NVML (libnvidia-ml) for the NVML-backed sensor

OS: Linux (x86_64, aarch64). CPU collection reads sysfs directly without sudo or an external executable. The toolkit GPU sensor is Linux-only; the catalog package compiles and runs on any platform.

## Quick start

Import the module:

```shell
go get github.com/dsx-ai-factory/health-validation@latest
```

Build the binary from source:

```shell
# Standard go build (local iteration)
go build -o health-validation ./toolkit/cmd/health-validation

# Canonical build via Bazel
bazel build //toolkit/cmd/health-validation
```

Run a one-shot check:

```shell
./health-validation run
```

The verdict's `node_id` defaults to the configured system hostname. Use
`--node-id <id>` to supply a scheduler or inventory identifier instead.
Optional `--cadence CONTINUOUS|PROLOG|MAINTENANCE`
runs checks scheduled for that window and reports the others as
`CADENCE_MISMATCH` skips; omit it to run the full stock catalog.
The execution context defaults to `TENANT_GUEST`. Use
`--execution-context OPERATOR_INFRA|TENANT_GUEST|OUT_OF_BAND` to select checks
for a different execution context.

On exit `0` or `1` a JSON verdict is written to stdout; on exit `2` no verdict was produced — the error goes to stderr and stdout is empty. The exit code is a coarse health signal (`0` = PASS/INFO/WARNING, `1` = CRITICAL/UNKNOWN, `2` = the tool could not produce a verdict); check `overall_severity` in the output for the precise level.

Run with a config file (threshold overrides):

```shell
./health-validation run --config operator.yaml
```

Collect sensor measurements without evaluating checks:

```shell
./health-validation collect > observations.json
```

For `collect`, an optional config's check IDs select the sensor domains to run. Without one, every built-in sensor domain runs. The output is one `ObservationBatch` (`collect/proto/health/v1/observation.proto`) in protojson form, with one observation per measurement field. Each signal ID is the domain plus the field name the checks read (e.g. `nvlink.inactive_count`); these are toolkit names, not canonical collect signal IDs. GPU measurements target the GPU and host-wide ones target the node (`--node-id`, defaulting to the hostname). Each observation carries a `value`, a `collectionError`, or a `noValue`; map and list fields are JSON-encoded strings. A signal the source reports as unsupported is a `noValue` with reason `NO_VALUE_REASON_NOT_SUPPORTED`, never a zero value. Sensor failures remain structured data and do not change the successful exit code.

Slurm prolog integration:

```shell
#!/bin/bash
# /etc/slurm/prolog.d/health-validation.sh
nodename="$SLURMD_NODENAME"
VERDICT=$(./health-validation run --node-id "$nodename")
EXIT=$?

# Exit 2: the tool could not produce a verdict (config/backend/collection error).
if [ $EXIT -eq 2 ]; then
  echo "health-validation failed to run" >&2
  exit 1
fi

# Exit 1: CRITICAL or UNKNOWN verdict. The JSON was still written, so read
# overall_severity for the drain reason.
if [ $EXIT -ne 0 ]; then
  SEVERITY=$(echo "$VERDICT" | jq -r '.overall_severity')
  scontrol update nodename="$nodename" state=drain reason="health-validation:$SEVERITY"
  exit 1
fi

# Exit 0: PASS/INFO/WARNING. Inspect overall_severity/skipped_checks if you
# want to gate WARNING or partial-coverage nodes more strictly.
exit 0
```

Direct evaluation (supply measurements from your own telemetry stack):

```go
import (
    "github.com/dsx-ai-factory/health-validation/catalog"
    "github.com/dsx-ai-factory/health-validation/catalog/node"
    "github.com/dsx-ai-factory/health-validation/catalog/health"
)

cat := catalog.New(
    catalog.WithArchitecture("GB200"),
    catalog.WithExecutionContext(health.OperatorInfra),
)

m := node.NodeMeasurement{GPUCount: 4}

verdict, err := cat.EvaluateAll(m)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("overall_severity=%s\n", verdict.OverallSeverity)
```

Sensor collection (collect from hardware and evaluate in one call):

```go
import (
    "github.com/dsx-ai-factory/health-validation/catalog"
    "github.com/dsx-ai-factory/health-validation/catalog/health"
    "github.com/dsx-ai-factory/health-validation/toolkit"
)

cat := catalog.New(
    catalog.WithArchitecture("GB200"),
    catalog.WithExecutionContext(health.OperatorInfra),
)

// backend implements toolkit.GPUBackend.
// On Linux, use nvml.New() from toolkit/nvml (requires cgo + NVML).
// For testing without hardware, use toolkit.MockGPU().
gpuSensor := toolkit.NewGPUSensor(backend)
nodeSensor := toolkit.NewNodeSensor(backend)
cpuSensor := toolkit.NewCPUSensor(toolkit.SysfsCPU())
// The Xid sensor reads the system journal, so it takes its own backend rather
// than the GPU one: toolkit.Journalctl() in production, toolkit.MockXid(...) in tests.
xidSensor := toolkit.NewXidSensor(toolkit.Journalctl())
verdict, err := toolkit.Collect(cat, gpuSensor, nodeSensor, cpuSensor, xidSensor)
```

Disable a specific check:

```go
cat := catalog.New(
    catalog.WithArchitecture("GB200"),
    catalog.WithDisabledChecks("gpu_xid_79"),
)
```

Disabled checks appear in `skipped_checks` with `skip_reason: CHECK_DISABLED`.

Override a Default threshold or disable a check via config file:

```yaml
# operator.yaml
checks:
  gpu_count:
    expected_gpu_count: 8
  nvlink_active:
    max_inactive_nvlinks: 0
  gpu_xid_79:
    occurrences: 2
  gpu_xid_149:
    fatal_occurrences: 1
    nonfatal_occurrences: 5
disabled_checks:
  - gpu_power_state
```

Every ID under `disabled_checks` is validated against the registered checks, so
a typo fails the run rather than leaving the check quietly enabled. Because
skipped checks are excluded from `overall_severity`, a disable narrows the
minimum bar the node is held to — read `skipped_checks`, not just the exit code,
to know what actually ran.

### Publishing metrics

The same config file can push the verdict to an OTLP/HTTP metrics endpoint —
a collector running on the node, or a remote one:

```yaml
# operator.yaml
metrics:
  enabled: true
  endpoint: http://127.0.0.1:44318   # full URL, not the collector's host:port form
  timeout_seconds: 10                # optional; defaults to 10
  headers:                           # optional; for an endpoint that authenticates values are passed as plain text since GPUs are considered privileged
    x-api-key: <SECRET>
  resource_attributes:               # optional; fleet context on every metric
    cluster: dsx-east
```

`endpoint` is a full URL. An OTLP collector's own receiver config writes
`127.0.0.1:44318`, so that form is the easy mistake — it is rejected at load
time with the fix spelled out. A URL with no path gets `/v1/metrics` appended;
an explicit path is used as given. `https` enables TLS. Any values that are
written to `headers` or `resource_attributes` are passed along as plain text
not as expanded environment variables.

Six metrics are defined, all published on every run.

| Metric | Type | Value | Attributes |
|---|---|---|---|
| `health_validation.node.severity` | gauge | `1`/`0` | `severity` |
| `health_validation.domain.severity` | gauge | `1`/`0` | `severity`, `category` |
| `health_validation.check.severity` | gauge | `1`/`0` | `severity`, `check_id`, `category`, `condition_scope`, `maturity_level`, `nvidia_endorsed` |
| `health_validation.check.skipped` | gauge | `1`/`0` | `check_id`, `category`, `skip_reason` |
| `health_validation.check.affected_entities` | gauge | Count of distinct entities a check named, `0` included | `check_id`, `category` |
| `health_validation.runs` | counter (delta) | `1` | `severity` |

The three severity gauges carry the severity as a **name** — `PASS`, `INFO`,
`WARNING`, `CRITICAL`, `UNKNOWN` — rather than encoding it as a number to
decode. Each run reports all five for every subject: `1` for the severity in
effect and `0` for the other four. So a critical node is

```
health_validation_node_severity{severity="CRITICAL"} 1
health_validation_node_severity{severity="PASS"}     0
```

and alerting on it is `health_validation_node_severity{severity="CRITICAL"} == 1`.

Emitting the zeros matters: if only the severity in effect were reported, the
previous run's `severity="CRITICAL"` point would sit at `1` with nothing to
contradict it, and a node that had recovered would keep reading as critical.

A check that runs per entity — most GPU and NVLink checks — produces one result
per GPU, so `check.severity` reports the **worst** severity across them and
`check.affected_entities` counts the distinct entities named. One failing GPU
therefore shows as a failing check; which GPU it was is in the JSON verdict's
`affected_entities`, not in the metrics.

`check.skipped` reports every check against every skip reason: `1` if it was
skipped for that reason, `0` if it was not.

`check.affected_entities` reports `0` for a check that ran and named nothing.

Node identity and versions ride on the OTLP **resource**
(`service.name`, `service.version`, `service.instance.id` = the node ID, plus
`health_validation.catalog_version` and `health_validation.schema_version`), not
on each data point. The per-run `trace_id` and `span_id` are deliberately not
published — they are unique every run and would mint a new time series per
execution; read them from the JSON verdict instead.

Publishing never changes the verdict or the exit code: an unreachable collector
is logged as a warning and the run reports exactly what it would have reported
otherwise. The verdict JSON is written to stdout before the push, so a slow
collector cannot delay it.

Where the metrics go and what is sent with them comes only from this config.
The OTLP exporter reads `OTEL_EXPORTER_OTLP_*` from the environment and applies
it *before* the options it is given, so every setting carrying a destination or
credentials — endpoint, headers, timeout, compression — is passed explicitly,
empty values included, to overwrite whatever the node's environment holds. A
stray `OTEL_EXPORTER_OTLP_HEADERS` cannot attach another endpoint's credentials
to yours. TLS trust material (`OTEL_EXPORTER_OTLP_CERTIFICATE` and the
client-certificate variables) is the deliberate exception, still read from the
environment: it is the only way to reach a collector behind a private CA, and
this config has no field for it.

> Running in a container? The stock wrapper does not use host networking, so
> `127.0.0.1` inside the container is not the node's loopback. See
> [docs/integration.md](docs/integration.md#publishing-metrics-from-a-container).

## Where to go for more

1. [docs/integration.md](docs/integration.md): deploy to a GPU cluster, wire into the workload lifecycle, act on verdicts, collect results across the fleet
2. [docs/remediation.md](docs/remediation.md): the remediation action catalog and how to work through a check's remediation sequence
3. [docs/architecture.md](docs/architecture.md): package roles, data flow, adding checks and sensors
4. [docs/extending.md](docs/extending.md): extend the catalog with your own private checks
5. [docs/developer.md](docs/developer.md): build, CI, container images, publishing
6. [docs/faq.md](docs/faq.md)
7. [catalog/health/node-verdict.schema.json](catalog/health/node-verdict.schema.json): JSON Schema for the node verdict output
8. [CONTRIBUTING.md](CONTRIBUTING.md)
9. [ROADMAP.md](ROADMAP.md): development focus areas and related work

## Maintainers

| Role | Responsibilities |
|---|---|
| Engineering Lead | Check engine architecture, sensor layer, Go module structure |
| Engineering Manager | Engineering planning and team coordination |
| TPM | Milestone tracking and release coordination |
| PM | Catalog scope and contribution program |

## Contributing

Contributions welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, check
authoring, and PR guidelines. All participation is governed by our
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

This project is licensed under the Apache License 2.0. See [LICENSE](LICENSE).

## Support

[GitHub Issues](https://github.com/dsx-ai-factory/health-validation/issues) is the
public support channel for bug reports and feature requests. See
[SUPPORT.md](SUPPORT.md) for other help options and response expectations.

Report security vulnerabilities privately as described in
[SECURITY.md](SECURITY.md). Do not file a public issue.
