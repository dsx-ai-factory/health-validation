# NVIDIA Health Validation

NVIDIA Health Validation runs on a GPU node, evaluates hardware and driver state against calibrated thresholds, and returns a structured verdict. The verdict answers one question: is this node ready to accept the next workload?

The repository is `github.com/NVIDIA/health-validation`. It is split into two independently versioned Go modules:

- **`catalog`** (`…/go-health-validation/catalog`): Check definitions, threshold logic, severity classifications, and a CEL-based evaluation engine. No hardware dependency. Import this if you already collect GPU telemetry and want NVIDIA's operational judgment applied to your signal stream.
- **`toolkit`** (`…/go-health-validation/toolkit`): Sensor implementations (NVML-backed GPU sensor, mock backends for testing) and a `Collect()` function that runs sensors, evaluates all checks, and returns a node-level verdict. Import this if you want the full working sensor and evaluation stack.

`toolkit` depends on `catalog`; import `toolkit` to get both.

## What it does

The check engine collects typed measurements from sensors, evaluates them against catalog-defined CEL rules and thresholds, and aggregates the results into a node-level verdict. Each check produces a per-check result. The node-level verdict is the worst severity across all checks that ran.

### Example: clean node

```json
{
  "timestamp": "2026-06-05T14:32:01Z",
  "overall_severity": "PASS",
  "severity_by_domain": {},
  "completion_ratio": 1,
  "check_results": [
    {
      "timestamp": "2026-06-05T14:32:01Z",
      "check_id": "gpu_count",
      "severity": "PASS",
      "category": "node",
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
  "timestamp": "2026-06-10T14:31:12.337336529-05:00",
  "severity_by_domain": {
    "5": "CRITICAL",
    "6": "CRITICAL"
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
        "gpu_count": 1,
        "nvlink_inactive_count": 0
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
      "recommended_action": "reboot",
      "messages": [
        "Expected 4 GPUs, found 1."
      ],
      "severity": "CRITICAL",
      "impact_scope": "NODE",
      "category": "node",
      "skipped": false,
      "nvidia_endorsed": true
    },
    {
      "timestamp": "2026-06-10T14:31:12.331524621-05:00",
      "observed_values": {
        "gpu_count": 1,
        "nvlink_inactive_count": 0
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
      "severity": "PASS",
      "category": "node",
      "skipped": false,
      "nvidia_endorsed": true
    }
  ],
  "skipped_checks": []
}
```

The process exit code is a coarse health signal: `0` for PASS/INFO/WARNING, `1` for CRITICAL/UNKNOWN, and `2` when the tool could not run to a verdict (config/backend/collection/output failure). It does not distinguish CRITICAL from UNKNOWN or INFO from WARNING, and it does not reflect `skipped_checks`/`completion_ratio` — read `overall_severity` in the JSON for the precise level.

## How it works

```
Sensor.Measure() --> []Measurement --> Catalog.EvaluateAll() --> NodeVerdict
```

Sensors collect typed measurements from hardware. Each sensor produces one Measurement per device (one per GPU, one per node). The toolkit includes a GPU sensor backed by NVML and mock constructors for testing. Sensors that cannot observe a device return an errored measurement rather than failing the whole run; the catalog decides per-check whether to surface a CRITICAL verdict or skip.

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

Five checks are currently registered:

| Check ID | Category | What it evaluates | Configurable thresholds |
|---|---|---|---|
| `gpu_count` | node | Number of GPUs present on the node | `expected_gpu_count` |
| `nvlink_active` | node | Number of active NVLink connections | `max_inactive_nvlinks` |
| `xid` | xid | Xid catalog checks | `gpu_xid_XID` |

Each check definition carries:

- `execution_contexts`: where the check can run (OPERATOR_INFRA, TENANT_GUEST)
- `category`: the hardware domain (gpu, node, nvlink, pcie, infiniband, storage)
- `provenance`: `nvidia` for all built-in checks
- `maturity_level`: stable or experimental

## Prerequisites

- Go 1.25.1+
- NVIDIA driver and nvidia-smi in PATH (required for the GPU sensor; not required if you supply measurements directly)
- NVML (libnvidia-ml) for the NVML-backed sensor

OS: Linux (x86_64, aarch64). The toolkit GPU sensor is Linux-only; the catalog package compiles and runs on any platform.

## Quick start

```shell
```

Import the module (catalog + toolkit):

```shell
go get github.com/NVIDIA/health-validation@latest
```

Build the binary from source:

```shell
# Standard go build (local iteration)
go build -o go-health-validation ./toolkit/cmd/go-health-validation

# Canonical build via Bazel
bazel build //toolkit/cmd/go-health-validation
```

Run a one-shot check:

```shell
./go-health-validation run
```

The verdict's `node_id` defaults to the configured system hostname. Use
`--node-id <id>` to supply a scheduler or inventory identifier instead.

On exit `0` or `1` a JSON verdict is written to stdout; on exit `2` no verdict was produced — the error goes to stderr and stdout is empty. The exit code is a coarse health signal (`0` = PASS/INFO/WARNING, `1` = CRITICAL/UNKNOWN, `2` = the tool could not produce a verdict); check `overall_severity` in the output for the precise level.

Run with a config file (threshold overrides):

```shell
./go-health-validation run --config operator.yaml
```

Slurm prolog integration:

```shell
#!/bin/bash
# /etc/slurm/prolog.d/health-validation.sh
nodename="$SLURMD_NODENAME"
VERDICT=$(./go-health-validation run --node-id "$nodename")
EXIT=$?

# Exit 2: the tool could not produce a verdict (config/backend/collection error).
if [ $EXIT -eq 2 ]; then
  echo "go-health-validation failed to run" >&2
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
    "github.com/NVIDIA/health-validation/catalog"
    "github.com/NVIDIA/health-validation/catalog/node"
    "github.com/NVIDIA/health-validation/catalog/health"
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
    "github.com/NVIDIA/health-validation/catalog"
    "github.com/NVIDIA/health-validation/catalog/health"
    "github.com/NVIDIA/health-validation/toolkit"
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
xidSensor := toolkit.NewXidSensor(backend)
verdict, err := toolkit.Collect(cat, gpuSensor, nodeSensor, xidSensor)
```

Disable a specific check:

```go
cat := catalog.New(
    catalog.WithArchitecture("GB200"),
    catalog.WithDisabledChecks("xid_sensor_79"),
)
```

Disabled checks appear in `skipped_checks` with `skip_reason: CHECK_DISABLED`.

Override a Default threshold via config file:

```yaml
# operator.yaml
checks:
  gpu_count:
    expected_gpu_count: 8
```

## Where to go for more

1. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md): package roles, data flow, adding checks and sensors
2. [docs/DEVELOPER.md](docs/DEVELOPER.md): build, CI, container images, publishing
3. [docs/FAQ.md](docs/FAQ.md)
4. [CONTRIBUTING.md](CONTRIBUTING.md)

## Maintainers

| Role | Responsibilities |
|---|---|
| Engineering Lead | Check engine architecture, sensor layer, Go module structure |
| Engineering Manager | Engineering planning and team coordination |
| TPM | Milestone tracking and release coordination |
| PM | Catalog scope and contribution program |

## Contributing

Contributions not yet open. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0. See LICENSE.

## Reporting issues

File an issue at <https://github.com/NVIDIA/health-validation/issues> with the check ID, observed behavior, hardware platform, driver version, and NVML version.
