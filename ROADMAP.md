# Health Validation Roadmap

Health Validation evaluates a node's hardware and driver state and returns a
structured verdict for the next workload. This roadmap groups operational
feedback and development proposals into three focus areas.

## How to read this roadmap

The focus areas below describe proposed direction. Linked issues provide
context on requests and decisions. Priority, scope, and sequencing may change
as feedback and validation results come in. Inclusion here does not establish
an implementation or release commitment.

For current capabilities, see the [README](README.md) and
[check catalog](catalog/checks.go). [SUPPORT.md](SUPPORT.md) defines supported
release lines and compatibility expectations;
[release notes](https://github.com/dsx-ai-factory/health-validation/releases)
record what has shipped.

## Focus areas

| Focus area | Outcome |
| --- | --- |
| [Make verdicts and recommendations more useful](#make-verdicts-and-recommendations-more-useful) | Operators can distinguish current faults from historical evidence and understand the recommended next steps. |
| [Adopt shared sensors and an agent API](#adopt-shared-sensors-and-an-agent-api) | Consumers can reuse sensor data and request node verdicts through the CLI or a long-running agent. |
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
  collection failure, while retaining evidence of the failure.

Validation should cover active faults, historical events, collection failures,
and evaluation after recovery. Health Validation produces findings and
recommendations; the calling system owns scheduling and remediation.

### Adopt shared sensors and an agent API

Migrate hardware acquisition to the sensors in `collect`, the shared collection
foundation for Health Validation, Fleet Intelligence, and GPUd. Keep thresholds,
severity, and health judgment in the catalog.

- **Shared acquisition.** Complete the migration of GPU, host, NVLink, and
  journal-backed collection to common sensors, reusing hardware sources and
  inventory across domains within each validation run.
- **Collection for applicable checks.** Request only the signals required by
  enabled checks that apply to the node's architecture, execution context, and
  selected cadence.
- **Agent mode.** Integrate `agentkit` to add a long-running agent with a gRPC
  API for node health verdicts on demand, alongside the one-shot CLI.
- **Repeatable evaluation.** Evaluate captured observations with catalog policy
  so consumers can reproduce findings without access to the original hardware.

Preserve observation identity, units, timing, and the distinction between values,
unavailable values, and collection errors. Validate the migration against existing
check coverage, verdicts, recommended actions, and skip behavior on relevant
hardware. Review API compatibility and document prerequisites before replacing a
collection path.

Captured and live evaluation should agree when measurements, node architecture,
and evaluation settings are equivalent. Broader collection does not by itself
expand the catalog's check coverage.
The `collect` and `agentkit` modules remain experimental under the current
[support policy](SUPPORT.md#support-lifecycle).

### Extend validated check coverage

Expand coverage where operational feedback identifies a useful check, and make
its prerequisites and limits clear.

- **Hardware applicability.** Validate checks for additional architectures and
  topologies before expanding coverage claims.
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
Track implementation progress and priority decisions in issues and pull requests.
