# FAQ

## What does the process exit code mean?

The exit code is a coarse health signal:

| Exit | Meaning |
|---|---|
| <nobr>`0`</nobr> | Verdict is PASS, INFO, or WARNING — the node may remain in service. (WARNING maps to 0 today; this may become configurable.) |
| <nobr>`1`</nobr> | Verdict is CRITICAL or UNKNOWN — the node should be removed from service, or its health could not be assessed. |
| <nobr>`2`</nobr> | The tool could not run to a verdict at all — a config error, backend/sensor initialization failure, collection failure, threshold-override audit failure (journald socket unavailable or a write failed), or output write error. No verdict was produced. |

The exit code does not distinguish CRITICAL from UNKNOWN or INFO from WARNING. For the precise level, read `overall_severity` from the JSON — Slurm prolog scripts that need more than the coarse signal should do so.

## What happens if a sensor cannot read from a GPU?

It depends on how the sensor reports the failure. If the sensor can identify the GPU but cannot read one or more fields, it calls `AddError()` on the measurement for those failing fields. The catalog evaluates each check against the partial measurement: checks that require only the available signals proceed normally; checks that require a failed signal produce a CRITICAL verdict rather than silently passing.

If the sensor cannot identify the GPU at all (e.g. a device handle failure), it returns an `ErroredGPU` measurement. All checks for that GPU produce CRITICAL verdicts.

## What does `skipped: true` mean?

The check did not execute. The `skip_reason` field says why:

| Reason | Meaning | Action |
|---|---|---|
| NO_APPLICABLE_MEASUREMENT | The check's domain was collected, but no measurement in it carries the check's required signals. | Confirm the sensor produces the measurement shape this check needs. |
| SENSOR_MISSING | The check's whole domain supplied no measurement: the sensor is absent, unconfigured, or its backend was never constructed. | Confirm the domain's sensor ran. Note this is not a collection *failure* — a sensor that ran and failed yields a CRITICAL result, not a skip. |
| SIGNAL_UNAVAILABLE_ARCHITECTURE | The check does not apply to this GPU architecture. | None. The check will never run in this environment. |
| SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL | The check requires a different execution context. | Check `skip_detail` for the required context, then set `--execution-context` to match. |
| CHECK_DISABLED | An operator disabled this check, through `disabled_checks:` in the config or `catalog.WithDisabledChecks`. | Intentional, but the verdict does not record which config file disabled it. |
| INSUFFICIENT_HISTORY | Time-window data is not yet available. No currently registered check uses this reason. | Re-run after the window accumulates. |
| THRESHOLD_UNCONFIGURED | A required-override threshold (one the operator must supply, with no universal default) was not configured. | Set it in the config file for this fleet; `skip_detail` names the threshold. |
| CADENCE_MISMATCH | The check is not scheduled for the requested run window. | None. The check belongs to another cadence. |

Skipped checks are excluded from `overall_severity`, so a node can look healthy while some checks never ran. Inspect `skipped_checks` as well as `overall_severity`, and investigate any node where every check was skipped.

## How do I run only a subset of checks?

List the check IDs under `disabled_checks:` in the config file passed to `--config`:

```yaml
disabled_checks:
  - gpu_power_state
  - gpu_xid_79
```

Each ID is validated against the registered checks, so a typo is an error rather than a check that silently keeps running. Disabled checks appear in `skipped_checks` with `skip_reason: CHECK_DISABLED` and are excluded from `overall_severity` — a disable lowers the bar the node is measured against, so it is worth reviewing what is in that list.

The equivalent for a consumer building their own binary is `catalog.WithDisabledChecks()` at catalog construction time. There is still no positive filter (no "run only these"); disabling is subtractive.

To select checks by scheduling cadence, use the optional `--cadence` flag. Each check declares a `SchedulingCadence` (CONTINUOUS, PROLOG, or MAINTENANCE), and the windows nest (CONTINUOUS ⊂ PROLOG ⊂ MAINTENANCE): PROLOG runs the CONTINUOUS and PROLOG checks, and MAINTENANCE runs every check. Checks outside the requested window appear in `skipped_checks` with `skip_reason: CADENCE_MISMATCH`. **Omit the flag and every registered check still runs**, including Prolog checks such as `gpu_power_limit`. Job-time integrators that must not run Prolog checks pass `--cadence=CONTINUOUS`. Cadence is not a YAML config field: one cluster config can serve every window.

## Can I add custom checks?

Yes. The catalog is open for extension. Implement a `health.CheckDefinition`, define a measurement struct that implements `health.Measurement`, and register the check via `catalog.Register()` or by adding it to `builtinChecks()` in a fork. The evaluation engine treats built-in and custom checks identically.

Custom checks you register carry `provenance: private`. Check results set `nvidia_endorsed: false` for any check whose provenance is not `nvidia`, so your tooling can filter to NVIDIA-authored checks without custom logic.

The community contribution program (allowing checks to ship with `provenance: community` in the NVIDIA-maintained catalog) is not yet open.

## Are thresholds adjustable?

It depends on the `ThresholdCategory`:

- **DEFAULT**: Operator can override via config file. NVIDIA's value is used if no override is supplied.
- **ADJUSTABLE**: Operator can override within documented bounds.
- **FIXED**: Non-overridable. Attempting to override returns an error before the check runs.

Two further constraints apply to any override the category permits. It must match the threshold's declared `value_type`, and it must fall within `value_min`/`value_max` when those are published. Both are rejected before the check runs rather than producing a misleading verdict — a threshold compared for equality against a nonsensical value would otherwise fail on every node. A value that converts cleanly is accepted, so an integer supplied for a `FLOAT64` threshold is fine; one that would lose information, such as `3.5` for an `INT64` threshold, is not.

Not every threshold publishes bounds, and an absent `value_max` is deliberate rather than an omission. Where the threshold *is* the tolerance — the maximum inactive NVLinks you accept, say — raising it is how you ask not to be alerted, so capping it would remove a legitimate control.

All active overrides appear in `thresholds[name].overridden: true` in the output, alongside the original NVIDIA value in `thresholds[name].original_value`. Each override is also recorded in the host's systemd journal; see [Audit trail for overrides](/health-validation/operating/integration#audit-trail-for-overrides).

## What Go version is required?

Go 1.27.3, as declared in `go.mod`. The repo is a single Go module; `catalog`, `toolkit`, `collect`, and `agentkit` are packages within it.

## What hardware is covered?

The catalog currently centers on GB200 and GB300 GPU nodes, with some checks scoped to further NVIDIA architectures, plus host CPU availability and topology. Registered checks cover host CPU (possible-versus-online logical CPU count and online NUMA node count), GPU power (power limit, power state, average draw, hardware throttle), leftover GPU compute processes, GPU count, NVLink (active links, fabric health, fabric status, bandwidth/count/speed, capabilities, and peer-to-peer), fatal mlx5 RDMA link flaps, and Xid-driven GPU events (a CRITICAL check per critical Xid code, a consolidated Xid warning check, and an INFO check for Xid 154). See the README's GB300 support note — GB200 coverage has been validated, GB300 coverage has not yet completed the same validation pass.

The sensor interface is extensible. Partners can implement `toolkit.GPUBackend` for other GPU types and supply measurements directly to `cat.EvaluateAll(m)` without modifying catalog source. NVIDIA does not author or validate checks for non-NVIDIA hardware.

## Can I test without a GPU?

Yes. The toolkit ships mock constructors: `toolkit.MockGPU(measurements...)` returns a `GPUBackend`, and `toolkit.MockCPU(measurement)` returns a `CPUBackend`. Pass them to their matching sensor constructors and call `toolkit.Collect(cat, sensor)` normally.

The catalog itself has no hardware dependency. Populate `gpu.GPUMeasurement` directly and call `cat.EvaluateAll(m)` for fully in-process testing.
