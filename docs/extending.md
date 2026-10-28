# Extending the catalog with private checks

This guide is for **downstream consumers** who want to run their own hardware
health checks — proprietary thresholds, vendor-specific conditions, signals the
public catalog does not carry — *on top of* health-validation, without
forking it.

The pattern is: keep your consumer code in its own module that imports this one,
register your check definitions with the catalog, and supply the signals they
read. The snippets below are self-contained; nothing here requires changes to
health-validation itself.

## What is stable, and what is not

> [!IMPORTANT]
> **Build against the catalog. Treat the toolkit's sensor/backend layer as a
> demonstration.**
>
> - The **catalog** — the `catalog/health` vocabulary (`CheckDefinition`,
>   `Rule`, `ThresholdSpec`, the `Measurement` interface, the enums) and the
>   `catalog` registry/evaluation engine — is the extension surface. Your check
>   definitions are data written against it.
> - The **toolkit** sensor and backend implementations (the NVML/sysfs/journal
>   sensors, the `toolkit.Collect` pipeline, the `app.App` builder, and the
>   probe seams shown below) are **reference implementations for demonstration
>   and are subject to change.** They are functional today, but do not treat
>   their API as stable; expect the collection wiring to churn.

The practical consequence: put your *judgment* (what condition is unhealthy, at
what threshold, with what remediation) in check definitions, which are durable.
Keep your dependence on the collection wiring thin and localized so it is cheap
to follow when it changes.

## A check is data

A check is a `health.CheckDefinition` — no code, no hardware dependency. The
engine evaluates its CEL `Rules` against signal values a measurement supplies.

```go
package mychecks

import "github.com/dsx-ai-factory/health-validation/catalog/health"

func GPUClockCapped() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_clock_capped",
		Name:        "GPU Clock Capped",
		Description: "Flags a GPU whose sustained clock is pinned below the expected floor.",
		Category:    health.GPU,

		// The signals this check reads. A measurement must carry all of them
		// (see "Routing") for the check to run.
		RequiredSignals: []string{"gpu_id", "sm_clock_mhz"},

		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Architectures:     []string{"GB200", "H100"},

		// Your checks stay yours: the engine honors this classification as
		// written. Use health.Private, the closest provenance for a definition
		// authored outside the public catalog; it excludes your checks from
		// canonical-only filters.
		Provenance: health.Private,

		// Operator-tunable threshold. FixedThreshold values cannot be overridden.
		// ValueType is mandatory; ValueMin/ValueMax are optional inclusive
		// bounds that an override must also satisfy.
		Thresholds: map[string]health.ThresholdSpec{
			"min_sm_clock_mhz": {
				Value:       int64(1200),
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Minimum acceptable sustained SM clock.",
				Unit:        "MHz",
			},
		},

		Rules: []health.Rule{
			{
				Expression:        "sm_clock_mhz < min_sm_clock_mhz",
				Severity:          health.Critical,
				Message:           "GPU ${gpu_id}: SM clock ${sm_clock_mhz} MHz below floor ${min_sm_clock_mhz} MHz.",
				RecommendedAction: health.GenericDrain(),
			},
		},
	}
}
```

Fields worth knowing:

- **`Category`** and **`RequiredSignals`** are the routing key — see below.
- **`Rules`** are CEL expressions over the union of the measurement's signal
  names and the check's threshold names. The worst-severity matching rule sets
  the verdict; `${name}` placeholders interpolate signal and threshold values.
- **`Thresholds`** are operator-tunable by default. `FixedThreshold` values can
  never be overridden; `RequiredOverride` means the check skips until a fleet
  supplies a value. Every threshold must declare a **`ValueType`** — CEL
  comparisons are type-sensitive, so the type is declared rather than inferred
  from whichever value happens to be present, and registration rejects a
  threshold without one. `ValueType` also types operator overrides, including
  on a threshold whose only values live in `PerArchitecture`. **`ValueMin`** and
  **`ValueMax`** are optional inclusive bounds, legal on the numeric types
  only; see [thresholds](#thresholds-types-and-bounds).
- **`RecommendedAction`**'s `RemediationActions` is an open `[]RemediationAction`
  (a string type, not a closed enum): use the `health.RemediationAction*`
  constants for steps NVIDIA's own catalog uses, or supply your own string for
  a remediation step specific to your extension. A downstream remediation
  engine should treat an unrecognized value as an opaque step, not an error.
- **`Provenance`**, `MaturityLevel`, and the other classification fields are
  passed through untouched. Use `health.Private`.
- **`ConditionScope`** records how far the detected condition reaches: `NodeScope`,
  `DomainScope`, or `ClusterScope`. It is `NodeScope` when omitted, so an existing
  check keeps its current meaning without change. Unlike the fields above it is
  not passed through untouched: a collection-failure verdict reports
  `NodeScope` whatever the check declares.

## Routing: how a measurement reaches your check

The engine feeds a measurement to a check when **both** hold:

1. they share a `Category`, and
2. the measurement's schema carries **every** one of the check's
   `RequiredSignals`.

Your check is then evaluated once per matching measurement (so a per-GPU check
yields one verdict per GPU). If no supplied measurement carries its required
signals, the check is skipped `NO_APPLICABLE_MEASUREMENT`; if its whole category
supplied no measurement at all, it is skipped `SENSOR_MISSING`. Registering the
check is what declares that its sensor is expected, so every registered check
produces a verdict — a result or a skip — and none disappears from the output.

So a private check only does something if **some measurement in its category
carries its signals.** That is the real work of extending: getting your signals
onto a measurement.

Two invariants keep routing unambiguous. Enforce both in your tests:

- **Signal names are unique within a category.** Two measurement types in the
  same category must not share a signal name, or a check could match the wrong
  one. Assert with `catalog.CheckSignalUniqueness(measurements...)`.
- **Every check has a provider.** A check whose signals no measurement carries
  can never run. Assert with `cat.CheckCoverage(measurements...)`.

```go
if err := catalog.CheckSignalUniqueness(mygpu.Measurement{}, /* … */); err != nil {
	t.Fatal(err)
}
if err := cat.CheckCoverage(mygpu.Measurement{}, /* … */); err != nil {
	t.Fatal(err)
}
```

### One measurement per check and entity

A check may receive at most one measurement per entity. If two measurements
that feed the same check describe the same entity, evaluation returns a
`health.AmbiguousMeasurementError` (matching `health.ErrAmbiguousMeasurement`)
naming the check and entity, and produces no verdict. The CLI exits `2`. The
engine never picks one provider over another, so two providers for one GPU, or
one provider emitting a GPU twice, is a wiring fault to fix. This applies even
when the two measurements are different Go types.

A measurement's entity is its `health.EntityKey`:

| Your measurement | Key |
|---|---|
| Implements `health.EntityKeyed` | Whatever `EntityKey()` returns |
| Implements only `health.EntityIdentifier` | `health.VerifiedEntity(EntityID())`, or unidentified when the ID is empty (see below) |
| Implements neither (host-wide) | `health.NodeEntity()`: one per run, whatever node ID the producer recorded |

Implement `EntityIdentifier` on any type that describes one of several like
entities. If you don't, every instance is keyed as the node, and two of them
are rejected as duplicates. Implement `EntityKeyed` when your ID is not a
stable hardware identity:

- `health.ProvisionalEntity(source, slot)` is for an entity whose identity
  lookup failed, named by the slot its source enumerated. Both values are
  required, and NVML's slot 0 and DCGM's slot 0 are different keys. A
  provisional measurement must report a collection failure on every check it
  feeds, because nothing ties its values to a verified GPU.
- `health.SourceEntity(source)` is for a whole-source failure that names no
  entity. The same rule applies.

An unidentified key (an empty `EntityID()`, or a zero `health.EntityKey`) is
allowed only for a collection failure:

- **Allowed:** an unidentified measurement whose errors fail a signal the check
  reads, such as a GPU whose UUID lookup failed. The check fails CRITICAL as a
  collection failure. Two of them feeding one check are still rejected, since
  nothing tells them apart.
- **Rejected:** an unidentified measurement carrying values the check reads,
  even from the only provider. Nothing shows it isn't a GPU another provider
  also reports, as when an ID-less `nvidia-smi` path is left running beside
  NVML. Set an ID or implement `EntityKeyed`.

The built-in `gpu.GPUMeasurement` and `nvlink.NVLinkMeasurement` take a key
through `SetEntityKey`. Without one, a non-empty `GPUID` is treated as verified
and an empty one as unidentified.

## Thresholds: types and bounds

Each threshold declares the Go type of its values. CEL is type-sensitive, and a
threshold can legitimately carry no `Value` at all — a per-architecture default
lives only in `PerArchitecture`, and a `RequiredOverride` threshold has no value
until a fleet supplies one. So the type is declared rather than inferred from
whichever value is present:

| `ValueType` | Go type | Accepts bounds |
|---|---|---|
| `health.ThresholdInt64` | `int64` | yes |
| `health.ThresholdFloat64` | `float64` | yes |
| `health.ThresholdBool` | `bool` | no |
| `health.ThresholdString` | `string` | no |
| `health.ThresholdStringList` | `[]string` | no |

Registration rejects a threshold that declares no type, and normalizes every
value to the declared one. That normalization is what lets you write the plain
literal `1200` rather than `int64(1200)` and still have CEL compare an `int64`.
An operator override is coerced the same way, so a YAML integer lands on a
`float64` threshold as a `float64`. A value that cannot convert without loss —
`3.5` for an `int64` threshold, or any string for a numeric one — is an error,
not a silent truncation.

`ValueMin` and `ValueMax` are optional inclusive bounds, independently settable,
and legal only on the two numeric types. They apply to the declared default,
every `PerArchitecture` entry, and any override.

Bound a threshold when an out-of-range value is unambiguously a mistake rather
than an unusual policy. A value compared for equality is the clear case: a GPU
performance state is bounded `0`–`15` because NVML defines no others, and a
negative expected count would make the rule fire on every node. **An upper-bound
tolerance is the opposite case** — setting one very high is how an operator asks
not to be alerted, so those declare a floor and no ceiling. Leaving `ValueMax`
unset is a deliberate choice, not an oversight.

An `AdjustableThreshold` must declare at least one bound. That category promises
operators a documented tunable range; without a bound it is indistinguishable
from `DefaultThreshold`.

## Getting your signals collected (demonstration wiring — subject to change)

> The mechanics in this section use the toolkit's sensor/backend layer, which is
> demonstration-grade and **subject to change.** Keep this wiring thin.

There are two situations.

**1. Your signals come from a domain the toolkit already enumerates** (e.g. GPU
via NVML). Attach a *prober* to the shared backend so the existing
category-measurement carries your extra signals. A prober implements
`nvml.DeviceProber` (`Measure(ProbeContext) health.Measurement` plus an
`Errored` fallback); it reads per-device state into your own measurement type
whose `Category()` is `health.GPU` and whose struct fields (via `json` tags)
are exactly your check's `RequiredSignals`. Install it on the app's shared NVML
backend:

```go
// Layer your prober onto the one NVML enumeration the node and NVLink
// sensors already share — no second session.
a = a.WithGPUBackend(a.NVMLBackend().WithGPUProbe(mygpu.Probe()))
```

**2. Your signals come from a new source.** You would implement collection here,
but this is exactly the layer flagged as unstable — approach it knowing the
interfaces may move.

## Registering and running your checks

The app builder is the demonstration entry point. `WithChecks` registers your
definitions alongside the built-ins (declared classification honored; a
duplicate ID fails):

```go
func main() {
	a := app.New().WithChecks(mychecks.All()...)
	a = configureGPU(a) // installs your prober; keep this platform-split (see below)
	os.Exit(a.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

If you do not want the node runner at all, use the catalog directly — the most
stable path:

```go
cat := catalog.New(
	catalog.WithArchitecture("GB200"),
	catalog.WithExecutionContext(health.OperatorInfra),
	catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID: "gpu_clock_capped", ThresholdName: "min_sm_clock_mhz", Value: int64(1400),
	})),
)
_ = cat.Register(mychecks.GPUClockCapped())
verdict, err := cat.EvaluateAll(measurements...) // measurements you produced yourself
```

## A recommended consumer layout

Keep your module thin and mirror the public per-domain structure. For a private
GPU vertical, that is roughly:

| File | Role |
|---|---|
| <nobr>`gpu/measurement.go`</nobr> | Your measurement type (`Category() == health.GPU`) whose `json`-tagged fields are exactly your checks' `RequiredSignals`. |
| <nobr>`gpu/*.go`</nobr> | One `health.CheckDefinition` per condition. |
| <nobr>`gpu/checks.go`</nobr> | An `All()` that returns every definition; passed to `app.WithChecks`. |
| <nobr>`gpu/probe_linux.go`</nobr> | Your `nvml.DeviceProber` that reads the signals into the measurement. |
| `main.go` + `backend_{linux,other}.go` | Wires `WithChecks` and `WithGPUProbe`, **platform-split** — the NVML prober is linux+cgo, so gate it behind `//go:build linux` and provide a no-op `configureGPU` for other platforms, so the binary still builds and `version` runs off-linux. |

health-validation owns the NVML lifecycle, device enumeration, and the
built-in sensors; your module owns only its measurement, its checks, and its
per-device read.

## Testing your extension

- Unit-test each check with `catalog.NewEmpty()`, `Register`, then `Evaluate`
  (single check) or `EvaluateAll` (batch) against measurements you construct
  directly — no hardware needed.
- Assert `CheckSignalUniqueness` and `CheckCoverage` over your full
  measurement-plus-check set, so a renamed signal or a check with no provider
  fails a test rather than silently never running.

See [Architecture](/health-validation/getting-started/architecture) for package roles and the evaluation data
flow.
