# FAQ

## What does the process exit code mean?

The exit code is a coarse health signal:

| Exit | Meaning |
|---|---|
| `0` | Verdict is PASS, INFO, or WARNING — the node may remain in service. (WARNING maps to 0 today; this may become configurable.) |
| `1` | Verdict is CRITICAL or UNKNOWN — the node should be removed from service, or its health could not be assessed. |
| `2` | The tool could not run to a verdict at all — a config error, backend/sensor initialization failure, collection failure, or output write error. No verdict was produced. |

The exit code does not distinguish CRITICAL from UNKNOWN or INFO from WARNING. For the precise level, read `overall_severity` from the JSON — Slurm prolog scripts that need more than the coarse signal should do so.

Note: the catalog library's `health.NodeVerdict.ExitCode()` method uses a different, older 0/1/2/3 scheme and is not what the CLI uses.

## What happens if a sensor cannot read from a GPU?

It depends on how the sensor reports the failure. If the sensor can identify the GPU but cannot read one or more fields, it calls `AddError()` on the measurement for those failing fields. The catalog evaluates each check against the partial measurement: checks that require only the available signals proceed normally; checks that require a failed signal produce a CRITICAL verdict rather than silently passing.

If the sensor cannot identify the GPU at all (e.g. a device handle failure), it returns an `ErroredGPU` measurement. All checks for that GPU produce CRITICAL verdicts.

## What does skipped: true mean?

The check did not execute. The `skip_reason` field says why:

| Reason | Meaning | Action |
|---|---|---|
| SIGNAL_UNAVAILABLE_ARCHITECTURE | Check does not apply to this GPU architecture. | None. The check will never run in this environment. |
| SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL | Requires a different execution context. | Check `skip_detail` for the required context. |
| CHECK_DISABLED | Operator disabled this check. | Intentional. |
| DEPENDENCY_SKIPPED | A prerequisite check was skipped or failed. | Investigate the dependency. |
| INSUFFICIENT_HISTORY | Time-window data not yet available. Not triggered by any currently registered check. | Re-run after the window accumulates. |
| THRESHOLD_UNCONFIGURED | A threshold the operator must supply was not configured (e.g. `ib_device_inventory.expected_devices`). | Set it in the config file for this fleet; `skip_detail` names the threshold. |

Skipped checks are excluded from `overall_severity`. A node where every check is skipped does not produce PASS; that requires investigation.

## How do I run only a subset of checks?

Use `catalog.WithDisabledChecks()` at catalog construction time, or set entries in the `checks:` config file with a flag that causes the check to skip (there is no CLI-level check filtering; filtering is done via catalog options).

For scheduling cadence, checks declare a `SchedulingCadence` field (PROLOG, CONTINUOUS, TENANT_TURNOVER, MAINTENANCE), but the CLI does not yet filter by cadence. All checks run on every invocation.

## Can I add custom checks?

Yes. The catalog is open for extension. Implement a `health.CheckDefinition`, define a measurement struct that implements `health.Measurement`, and register the check via `catalog.Register()` or by adding it to `builtinChecks()` in a fork. The evaluation engine treats built-in and custom checks identically.

Custom checks you register carry `provenance: private`. Health events set `nvidia_endorsed: false` for any check that is not `nvidia`, so your tooling can filter to NVIDIA-authored signals without custom logic.

The community contribution program (allowing checks to ship with `provenance: community` in the NVIDIA-maintained catalog) is not yet open.

## Are thresholds adjustable?

It depends on the `ThresholdCategory`:

- **DEFAULT**: Operator can override via config file. NVIDIA's value is used if no override is supplied.
- **ADJUSTABLE**: Operator can override within documented bounds.
- **FIXED**: Non-overridable. Attempting to override returns an error before the check runs.

All active overrides appear in `thresholds[name].overridden: true` in the output, alongside the original NVIDIA value in `thresholds[name].original_value`.

## What Go version is required?

Go 1.25.1. This is declared in each module's `go.mod` (the repo is split into the `catalog` and `toolkit` modules).

## What hardware is covered?

The catalog currently centers on GB200 GPU nodes, with some checks scoped to further NVIDIA architectures, plus host CPU availability and topology. Registered checks cover possible-versus-online logical CPU count, online NUMA node count, GPU count, GPU power state, NVLink active links, NVLink fabric health, and a set of XID-driven GPU events.

The sensor interface is extensible. Partners can implement `toolkit.GPUBackend` for other GPU types and supply measurements directly to `cat.EvaluateAll()` without modifying catalog source. NVIDIA does not author or validate checks for non-NVIDIA hardware.

## Can I test without a GPU?

Yes. The toolkit ships mock constructors: `toolkit.MockGPU(measurements...)` returns a `GPUBackend`, and `toolkit.MockCPU(measurement)` returns a `CPUBackend`. Pass them to their matching sensor constructors and call `toolkit.Collect()` normally.

The catalog itself has no hardware dependency. Populate `gpu.GPUMeasurement` directly and call `cat.EvaluateAll()` for fully in-process testing.
