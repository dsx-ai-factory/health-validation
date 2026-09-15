# Changelog

All notable changes to NVIDIA Health Validation are documented here.
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [catalog/v0.2.0-rc1] and [toolkit/v0.2.0-rc1] - 2026-09-15

Release candidate covering everything since `catalog/v0.1.0` and
`toolkit/v0.1.0`. Build, CI, and documentation-only changes are omitted.

### Breaking Changes

- The verdict JSON no longer carries a single recommended action. Each
  check now carries `remediation_actions`: an ordered sequence of steps to
  work through, re-running the whole suite between steps. `RecommendedAction.DiagnosticAction`
  is replaced by `RecommendedAction.RemediationActions`. See
  [`docs/REMEDIATION.md`](docs/REMEDIATION.md) for the consumption contract. ([#45](https://github.com/dsx-ai-factory/health-validation/pull/45))
- `Catalog.Evaluate` now fails closed unless an architecture is set with
  `WithArchitecture`. Previously an empty architecture disabled filtering and
  fell back to a threshold's base `Value`, which forced checks to carry
  placeholder defaults; `PerArchitecture` must now cover every declared
  architecture instead. ([#23](https://github.com/dsx-ai-factory/health-validation/pull/23))
- Checks are scoped to GB200 and GB300. H100 and A100 coverage is dropped
  for now, and the Xid checks that do not apply to GB200 are removed
  (54 auxiliary power, 74 NVLink pre-NVLink5, 80 corrupted data, 136 link
  training). ([#56](https://github.com/dsx-ai-factory/health-validation/pull/56))
- The completion-percentage threshold option and the `completion_ratio`
  metric are removed. ([#38](https://github.com/dsx-ai-factory/health-validation/pull/38))

### Features

- New `collect` and `agentkit` packages: a DCGM source that watches GPU
  fields, normalizes values and collection errors into observations, and
  guards against source re-entry after a collection timeout. Note that this is not yet
  wired into the core code path and is not yet included in the container image. ([#19](https://github.com/dsx-ai-factory/health-validation/pull/19))
- Remediation sequences now match confirmed downstream behavior on
  GB200/GB300: a generic reset → restart → escalate ladder for most checks,
  and a shorter two-step sequence where a host restart adds nothing. NVLink
  fabric faults gain a `hold` action, which holds affected nodes and escalates
  only once several nodes in the same NVLink/rack domain are held, rather
  than opening a case per node for one shared-switch fault. ([#51](https://github.com/dsx-ai-factory/health-validation/pull/51))
- New GPU checks: average power draw ([#18](https://github.com/dsx-ai-factory/health-validation/pull/18)) and sensor throttle ([#29](https://github.com/dsx-ai-factory/health-validation/pull/29)).
- Xid coverage for GB200-applicable codes that previously had none: 13
  (graphics engine exception) and 31 (memory page fault) join the
  application-level warning bucket, and 48 (double-bit ECC) gets its own
  CRITICAL check. ([#56](https://github.com/dsx-ai-factory/health-validation/pull/56))
- Verdicts carry a per-run `trace_id` and per-check `span_id` for correlating
  a run across systems. ([#17](https://github.com/dsx-ai-factory/health-validation/pull/17))
- Verdicts report the catalog version they were produced by. ([#24](https://github.com/dsx-ai-factory/health-validation/pull/24))
- Check definitions and verdicts carry a `condition_scope`. ([#26](https://github.com/dsx-ai-factory/health-validation/pull/26))
- A check whose sensor was not supplied is skipped with a distinct
  `SENSOR_MISSING` reason rather than being reported as a failure. ([#21](https://github.com/dsx-ai-factory/health-validation/pull/21))
- The container mounts the host's `journalctl` instead of bundling systemd,
  shrinking the image. Host journal access is now a deployment requirement —
  see the container journal access profiles in the docs. ([#15](https://github.com/dsx-ai-factory/health-validation/pull/15))

### Fixes

- A clean boot is no longer reported as an unreadable journal. `journalctl`
  exits non-zero *and* prints `-- No entries --` when a `--grep` matches
  nothing, which the emptiness test missed, so a healthy node could report
  CRITICAL with a drain recommendation. ([#31](https://github.com/dsx-ai-factory/health-validation/pull/31))
- GB300 is recognized by the checks that previously matched GB200 only. ([#12](https://github.com/dsx-ai-factory/health-validation/pull/12))
- RDMA link-flap detection uses a 1-hour lookback window and a boot offset,
  so flaps are attributed to the right window. ([#13](https://github.com/dsx-ai-factory/health-validation/pull/13))

## [catalog/v0.1.0] and [toolkit/v0.1.0] - 2026-08-17

### Features
- Initial open-source release of the `catalog` and `toolkit` Go modules.
- GPU health check catalog: `gpu_count`, `nvlink_active`, `gpu_nvlink_fabric_health`, and per-XID checks (`gpu_xid_*`).
- NVML-backed GPU sensor for Linux (`toolkit/nvml`).
- Operator CLI (`health-validation run`) producing structured JSON verdicts.
- CEL-based rule evaluation engine with operator-configurable threshold overrides.

[catalog/v0.2.0-rc1]: https://github.com/dsx-ai-factory/health-validation/tree/catalog/v0.2.0-rc1
[toolkit/v0.2.0-rc1]: https://github.com/dsx-ai-factory/health-validation/tree/toolkit/v0.2.0-rc1
[catalog/v0.1.0]: https://github.com/dsx-ai-factory/health-validation/tree/catalog/v0.1.0
[toolkit/v0.1.0]: https://github.com/dsx-ai-factory/health-validation/tree/toolkit/v0.1.0
