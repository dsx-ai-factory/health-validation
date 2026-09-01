# Changelog

All notable changes to NVIDIA Health Validation are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [catalog/v0.1.0] and [toolkit/v0.1.0] - 2026-08-17

### Added
- Initial open-source release of the `catalog` and `toolkit` Go modules.
- GPU health check catalog: `gpu_count`, `nvlink_active`, `gpu_nvlink_fabric_health`, and per-XID checks (`gpu_xid_*`).
- NVML-backed GPU sensor for Linux (`toolkit/nvml`).
- Operator CLI (`health-validation run`) producing structured JSON verdicts.
- CEL-based rule evaluation engine with operator-configurable threshold overrides.

[catalog/v0.1.0]: https://github.com/dsx-ai-factory/health-validation/tree/catalog/v0.1.0
[toolkit/v0.1.0]: https://github.com/dsx-ai-factory/health-validation/tree/toolkit/v0.1.0
