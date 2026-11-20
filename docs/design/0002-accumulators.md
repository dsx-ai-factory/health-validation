# 0002: Derive measurements with accumulators

| Field | Value |
| --- | --- |
| Status | Proposed |
| Authors | Wesley Graybill |
| Created | 2026-11-20 |
| Issue | To be opened |
| Design review | Add when opened |
| Supersedes | None |

## Summary

Replace `health.DerivedSignaler` with accumulators: catalog-registered
derivations that run inside evaluation, after collection and before the checks.
An accumulator sees every measurement a run collected, so it can join facts from
different measurement types (Xid events and the driver version) and aggregate
across entities (many ports into one node-wide value). Checks read the result
like any other signal. Reviewers need to decide whether this is the mechanism
the catalog uses for derived values, and whether the breaking removal of
`DerivedSignaler` is worth it.

## Problem and scope

A check reads one measurement. A lot of what checks need isn't something a
sensor reports directly though. It's an interpretation of what a sensor
reported, sometimes combined with something a different sensor reported.

Today the catalog has one way to do that. A measurement can implement
[`DerivedSignaler`][derived-signaler], and evaluation merges whatever
`DerivedSignals(host)` returns into that measurement's signals. That works as
long as the derived value depends only on the measurement itself, plus whatever
has been threaded through `health.Host`. We keep running into cases where it
doesn't:

- **Xid.** Subtype counts, recovery action counts ([#78]), and unknown and
  informational counts ([#260]) are all `DerivedSignals`. Decoding Xid 144-150
  ([#271]) also needs the driver version, so [#82] added `health.Host` and
  `catalog.WithDriverVersion` just to hand it to the derivation. Every new fact
  a derivation needs from outside its measurement means growing `Host` and
  plumbing it through the CLI.
- **Hooks on the measurement.** [#241] and [#260] needed a scoper so each
  verdict shows only its own events, and a table of the codes some check covers
  so `gpu_xid_unknown` knows what is unknown. That table only knows the built-in
  checks, so a consumer's own check on an Xid code double-reports with
  `gpu_xid_unknown`.
- **One check, many measurements.** `ib_port_state` reads one `PortMeasurement`
  per port. A check like "at least N compute-fabric rails are active at the
  expected rate" needs every port at once. `DerivedSignaler` can't express that,
  since it only sees the measurement it's attached to. [#256] builds a node-wide
  port measurement inside a toolkit adapter instead, the same way NVLink's
  `inactive_count` is aggregated in
  [`toolkit/observations/nvlink.go`][nvlink-aggregation]. That works for the
  stock CLI, but a consumer evaluating the catalog directly doesn't get it, and
  the rules for what the ports mean live away from the check.

Goals:

1. Derive values from more than one measurement: across types and across
   entities.
2. Keep derivation in the catalog, so the CLI and a consumer calling
   `EvaluateAll` directly get the same interpretation.
3. Checks don't know how a value was produced. A derived signal routes like any
   other.
4. A check never passes on a derived value that nobody derived.
5. Keep domain-specific hooks off the public `health` and `catalog` surface.

Non-goals: cross-node or historical derivation (the catalog stays node-local and
stateless), and moving the NVLink or InfiniBand toolkit aggregations in this
change. Those can follow if the mechanism is accepted.

Affected components: `catalog/health` and `catalog` (interface and evaluation),
`catalog/xid` (the first accumulator), `catalog/node` and the GPU inventory
adapter (the driver version becomes a collected signal), and `toolkit/app`
(collection planning).

## Design and rationale

An accumulator is registered with the catalog and runs inside evaluation. It
sees every measurement the run collected and returns new ones.

```go
type Accumulator interface {
    Name() string
    Inputs() []Measurement  // types it reads
    Outputs() []Measurement // types it produces
    Derived() []string      // signals it computes
    Accumulate([]Measurement) ([]Measurement, error)
}
```

An accumulator can do one of two things with its outputs:

- **Produce a new type.** For the port case, an accumulator would read every
  `PortMeasurement` and produce one node-wide measurement, and the check reads
  that. [#302] doesn't include this yet.
- **Refine an existing type.** If an output type is also one of its inputs, its
  outputs replace the collected measurements. The Xid accumulator returns each
  `XidMeasurement` with its derived fields filled in, joined with the driver
  version from `NodeMeasurement`. The GPU's identity, events and errors stay on
  the same measurement, so a check reading a derived signal still names the GPU
  and shows its events.

Guardrails keep this from failing quietly:

- A measurement type lists the signals only an accumulator fills in
  (`health.Refinable`). If a check needs one and nothing refines that type,
  evaluation errors and `CheckCoverage` flags the check, rather than the check
  reading an empty count and passing.
- Collection planning replaces a required derived signal with the accumulator's
  inputs, so a sensor is never asked for something it can't produce.
- Only one accumulator may refine a given type. `catalog.WithAccumulator`
  replaces a registered one by name, which is how a consumer configures the
  built-in Xid accumulator, for example declaring the codes their own Xid checks
  cover.
- When an input failed to collect, the accumulator carries that failure forward
  on its outputs. The error return is reserved for a fault in the accumulator
  itself and fails the evaluation.

Ownership stays where the layering puts it. Collect sensors and toolkit adapters
still turn raw observations into measurements, including any source-specific
meaning of an error. Accumulators work only on catalog measurements, so they
live in `catalog/<domain>` next to the checks that depend on them, and the
catalog keeps no dependency on collection or the toolkit.

This retires `DerivedSignaler`, `health.Host` and `catalog.WithDriverVersion`.
The driver version becomes an ordinary collected node signal
(`node.driver.version`).

## Alternatives and consequences

- **Keep `DerivedSignaler` and grow `Host`.** Least churn, but it doesn't solve
  multi-measurement checks or aggregation.
- **Aggregate in the toolkit** (as NVLink and [#256] do). Works for the stock
  CLI and is a reasonable stepping stone for a single check, but consumers who
  evaluate the catalog directly don't get it, and the toolkit's generic
  observation mapping grows a special case per domain.
- **A separate derived measurement type.** My first spike. Signal names have to
  be unique within a category, so the derived type couldn't also carry `gpu_id`
  or the events. Sub-code checks lost which GPU they were about, which [#231]
  and [#241] had just fixed.
- **Per-check evidence computed by the accumulator.** My second spike. It
  removed the scoper hook, but changed the shape of observed values for every
  Xid check and needed accumulators to see the full check set. I kept the scoper
  instead. It's internal to the module, so it isn't public surface.

Costs of the chosen approach:

- It's more concepts than one interface method: produce vs. refine, `Derived`,
  refined signals, and one refiner per type.
- Checks and their accumulator register separately. A catalog built with
  `NewEmpty` that registers Xid checks also needs `xid.Accumulator`. Evaluation
  fails loudly instead of silently, but it's one more thing to get right.
- Single-check `Evaluate` only sees the measurement it's handed, so it can't
  join anything (no driver version). That's fine today, but [#271]'s decode
  table will care.
- The guardrails catch a missing accumulator, not a buggy one. An accumulator
  that forgets to carry a failure forward can still produce a pass.
- Accumulators don't see checks, so per-check scoping of observed values keeps
  its internal hook.
- One refiner per type means unrelated derivations on `XidMeasurement` have to
  share one accumulator.

## Compatibility and delivery

`DerivedSignaler` shipped in `catalog/v0.3.2`, so removing it is a breaking Go
API change. `health.Host` and `catalog.WithDriverVersion` haven't been released.
Check IDs, signal names, verdict JSON and CLI behavior don't change.

Consumers building a catalog with `New` get the Xid accumulator automatically.
Consumers using `NewEmpty` with Xid checks register `xid.Accumulator`. Consumers
with their own Xid checks can declare the codes those checks cover with
`xid.Accumulator{CoveredCodes: ...}` and `catalog.WithAccumulator`.

Delivery is one implementation PR ([#302]). Moving the NVLink aggregation or a
port aggregation to accumulators would be separate follow-ups.

## Validation

Planned and already in [#302]:

- Framework tests: refined outputs replace their inputs, a missing accumulator
  errors and is reported by `CheckCoverage`, a second refiner for one type is
  rejected, and collection planning never requests a derived signal.
- Xid tests pass unchanged, including the identity and per-check events tests
  from [#231] and [#241], with no exemptions for checks reading derived signals.
- `CoveredCodes` tests for a consumer's own check on an unknown and an IGNORE
  code.
- `TestRunFixtureVerdicts` in `toolkit/app` runs a real Xid 149 line through
  collection, the accumulator and the sub-code check.
- The Linux CI configuration passes. No hardware testing is needed, since no
  collection behavior changes.

## Open questions and references

- Should codes a consumer declares for their own Xid check also hide that code
  from a built-in check, or only from the catch-all checks (what [#302] does
  today)?
- Does single-check `Evaluate` need a way to supply the other measurements an
  accumulator joins?
- Should a cross-port check like [#256] land as a toolkit aggregation first, or
  wait for accumulators?

References: [#82], [#78], [#231], [#241], [#256], [#260], [#271], [#302].

## Implementation

- [#302] (draft): accumulator framework, Xid accumulator, driver version as a
  node signal, and `CoveredCodes`.

[#78]: https://github.com/dsx-ai-factory/health-validation/pull/78
[#82]: https://github.com/dsx-ai-factory/health-validation/pull/82
[#231]: https://github.com/dsx-ai-factory/health-validation/pull/231
[#241]: https://github.com/dsx-ai-factory/health-validation/pull/241
[#256]: https://github.com/dsx-ai-factory/health-validation/pull/256
[#260]: https://github.com/dsx-ai-factory/health-validation/pull/260
[#271]: https://github.com/dsx-ai-factory/health-validation/pull/271
[#302]: https://github.com/dsx-ai-factory/health-validation/pull/302
[derived-signaler]: https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/measurement.go
[nvlink-aggregation]: https://github.com/dsx-ai-factory/health-validation/blob/main/toolkit/observations/nvlink.go
