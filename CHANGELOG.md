# Changelog

All notable changes to NVIDIA Health Validation are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `SENSOR_MISSING` skip reason: a check whose whole domain supplied no measurement — sensor absent, unconfigured, or backend never constructed — is now reported as a skip instead of silently producing no verdict. Distinct from `NO_APPLICABLE_MEASUREMENT` (domain collected, wrong measurement shape) and from a sensor that ran and failed (still a CRITICAL result).
- `Catalog.EvaluateAllExpecting` / `Catalog.EvaluateAllRawExpecting`, which take the categories the caller intended to cover, and `Catalog.Categories`, which lists the categories the registered checks span. `EvaluateAll` and `EvaluateAllRaw` are unchanged: they expect nothing, so a single-domain evaluation still raises no skips for the domains it did not ask about.

### Changed
- `NodeVerdict.schema_version` is now `0.2` (additive: one new `skip_reason` enum value).
- `toolkit.Collect` / `CollectWithTimeout` treat every category the catalog can check as expected, so a domain that reports nothing now yields `SENSOR_MISSING` skips. Those skips count toward the verdict's total, which lowers `completion_ratio`: a node missing a whole sensor may now cross a configured `WithCompletionThreshold` and report `overall_severity: UNKNOWN` where it previously reported a full-coverage result.

## [0.1.0] - 2026-08-17

### Added
- Initial open-source release of the `catalog` and `toolkit` Go modules.
- GPU health check catalog: `gpu_count`, `nvlink_active`, `gpu_nvlink_fabric_health`, and per-XID checks (`gpu_xid_*`).
- NVML-backed GPU sensor for Linux (`toolkit/nvml`).
- Operator CLI (`health-validation run`) producing structured JSON verdicts.
- CEL-based rule evaluation engine with operator-configurable threshold overrides.

[0.1.0]: https://github.com/dsx-ai-factory/health-validation/releases/tag/v0.1.0
