# Health Validation Roadmap

Health Validation evaluates a node's hardware and driver state and returns a
structured verdict for the next workload. This roadmap groups operational
feedback and development proposals into three focus areas.

## How to read this roadmap

The focus areas below describe proposed direction. Linked issues track
requests and decisions; linked pull requests show proposed implementations.
Priority, scope, and sequencing may change as feedback and validation results
come in. Inclusion here does not establish an implementation or release
commitment.

For current capabilities, see the [README](README.md) and
[check catalog](catalog/checks.go). [SUPPORT.md](SUPPORT.md) defines supported
release lines and compatibility expectations;
[release notes](https://github.com/dsx-ai-factory/health-validation/releases)
record what has shipped.

## Focus areas

| Focus area | Outcome |
| --- | --- |
| [Make verdicts and recommendations more useful](#make-verdicts-and-recommendations-more-useful) | Operators can distinguish current faults from historical evidence and understand the recommended next steps. |
| [Reuse observations across collection and evaluation](#reuse-observations-across-collection-and-evaluation) | Consumers can reuse collected measurements and apply catalog policy without duplicating collection or health logic. |
| [Extend validated check coverage](#extend-validated-check-coverage) | Operators can see which checks apply to their hardware and environment, with evidence for each coverage claim. |

### Make verdicts and recommendations more useful

Improve how findings explain a node's condition and guide the next evaluation.

- **Faults after recovery.** Define how evaluation distinguishes an active Xid
  condition from a historical event after a successful GPU reset
  ([#133](https://github.com/dsx-ai-factory/health-validation/issues/133)).
- **Action guidance.** Improve the
  [existing remediation guidance](docs/REMEDIATION.md) with concrete next steps
  and consistent advice on choosing an action when several checks fail
  ([#58](https://github.com/dsx-ai-factory/health-validation/issues/58)).
- **Collection failures.** Explain when several findings share an underlying
  collection failure, while retaining evidence of the failure. A proposed
  implementation is tracked in
  [PR #91](https://github.com/dsx-ai-factory/health-validation/pull/91).

Validation should cover active faults, historical events, collection failures,
and evaluation after recovery. Health Validation produces findings and
recommendations; the calling system owns scheduling and remediation.

### Reuse observations across collection and evaluation

Keep hardware collection reusable while the catalog owns thresholds, severity,
and health judgment. On current `main`, CPU collection uses the experimental
`collect` module, and the CLI can capture observations without evaluating health.

- **Shared collection.** Expand GPU and host measurements available through
  common collection interfaces
  ([PR #153](https://github.com/dsx-ai-factory/health-validation/pull/153)).
- **Collection for applicable checks.** Collect the signals needed by checks
  that can run in the selected context and cadence
  ([PR #156](https://github.com/dsx-ai-factory/health-validation/pull/156)).
- **Evaluation of captured observations.** Evaluate retained measurements with
  the catalog so consumers can reproduce findings without hardware access
  ([PR #164](https://github.com/dsx-ai-factory/health-validation/pull/164)).

The proposals need to preserve measurement identity, units, timing, and the
distinction between a value, an unavailable value, and a collection error.
Captured and live evaluation should agree when measurements, node architecture,
and evaluation settings are equivalent. Additional measurements require
separate check definitions before they contribute to a health verdict.
`collect` and `agentkit` remain
experimental under the current [support policy](SUPPORT.md#support-lifecycle).

### Extend validated check coverage

Expand coverage where operational feedback identifies a useful check, and make
its prerequisites and limits clear.

- **Hardware applicability.** Validate checks for additional architectures and
  topologies before expanding coverage claims. The B200 applicability proposal
  remains a draft in
  [PR #122](https://github.com/dsx-ai-factory/health-validation/pull/122).
- **Environment-specific guidance.** Document operational checks whose expected
  results depend on customer configuration. Explain required inputs,
  applicability, results, and next steps
  ([#57](https://github.com/dsx-ai-factory/health-validation/issues/57)).
- **Evaluation without host access.** Explore which checks can use out-of-band
  data and how consumers would supply it to the catalog
  ([#25](https://github.com/dsx-ai-factory/health-validation/issues/25)). No
  built-in check currently declares the `OUT_OF_BAND` context.

New coverage needs documented signal sources, applicable hardware and execution
contexts, reviewed thresholds, and validation on the relevant hardware. Callers
must inspect skipped checks as well as verdict severity when assessing coverage.

## Propose work or contribute

Comment on an existing issue, or use the
[Feature Request form](https://github.com/dsx-ai-factory/health-validation/issues/new?template=feature.yml)
to describe the operational problem, desired behavior, and evidence needed to
validate it. See [CONTRIBUTING.md](CONTRIBUTING.md) for contributions and
[GOVERNANCE.md](GOVERNANCE.md) for significant changes and architectural proposals.
Usage questions follow the routes in [SUPPORT.md](SUPPORT.md).

Update this roadmap when a focus area changes, is completed, or is retired.
Keep implementation progress and priority decisions in the linked issues.
