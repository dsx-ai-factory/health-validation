# Changelog

All notable changes to NVIDIA Health Validation are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-28

### Added
- Initial open-source release of the `catalog` and `toolkit` Go modules.
- GPU health check catalog: `gpu_count`, `nvlink_active`, `gpu_nvlink_fabric_health`, and per-XID checks (`gpu_xid_*`).
- NVML-backed GPU sensor for Linux (`toolkit/nvml`).
- Operator CLI (`go-health-validation run`) producing structured JSON verdicts.
- CEL-based rule evaluation engine with operator-configurable threshold overrides.

[0.1.0]: https://github.com/NVIDIA/health-validation/releases/tag/v0.1.0
