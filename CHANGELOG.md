# Changelog

All notable changes to NVIDIA Health Validation are documented here.
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Breaking Changes

- Verdict `schema_version` is now `0.6`. `affected_components` lists each
  failing check once, under `checks`, with that check's `entities`. The
  parallel `check_ids` and `entities` arrays are removed.
  ([#210](https://github.com/dsx-ai-factory/health-validation/issues/210))
- Verdict `schema_version` is now `0.5`. Collection failures report `UNKNOWN`
  instead of `CRITICAL`. Update alerts that match collection failures as
  `CRITICAL` to handle `UNKNOWN`. `CRITICAL` now ranks above `UNKNOWN`, and the
  Go `Critical` and `Unknown` constants swap numeric values. ([#267](https://github.com/dsx-ai-factory/health-validation/pull/267))
- The `catalog`, `toolkit`, `collect`, and `agentkit` modules are merged into
  the single root module `github.com/dsx-ai-factory/health-validation`, with one
  `go.mod` and one version. Import paths are unchanged. Replace requires on
  `…/catalog`, `…/toolkit`, `…/collect`, and `…/agentkit` with one require on the root module
  at the first `vX.Y.Z` release. Releases are tagged `vX.Y.Z` instead of
  `catalog/vX.Y.Z` and `toolkit/vX.Y.Z`, and the verdict's `catalog_version`
  equals the CLI version.
- The public `catalog/version` package is removed. Read the version from a
  verdict's `catalog_version`. In unstamped builds, `health-validation version`
  now reports the module version Go embeds, or `dev+<commit>`, instead of `dev`.
- The CLI reads the GPU domain from DCGM, through a hostengine running on the
  node, and from NVML only for compute processes. Nodes need `nv-hostengine`
  and the DCGM 4 client library (`libdcgm.so.4`); without them the GPU power,
  power-limit, pstate, and throttle checks report a collection failure. There
  is no fallback to NVML.
- The CLI reads the NVLink domain through collect sensors: fabric
  registration from DCGM, and link state, count, speed, capabilities, P2P,
  and fabric health from NVML. Without a reachable hostengine, `nvlink_fabric`
  reports a collection failure. Other differences from the legacy NVLink
  sensor, from the conversion added in
  [#237](https://github.com/dsx-ai-factory/health-validation/pull/237):
  - A driver reporting `NOT_SUPPORTED` past a GPU's last link no longer fails
    `nvlink_capabilities`, and a disabled link past the reported count no
    longer fails `nvlink_active`: links past the count are not read.
  - An unknown fabric health value fails `nvlink_fabric_health` with
    `check_collection` instead of passing.
  - Through the library, a node with no GPUs fails every NVLink check with
    `check_collection` instead of skipping it. The CLI is unchanged: it stops
    at `architecture_discovery` before running any check.

  Library callers migrating:
  - The collect adapter name `nvlink` is reserved for the built-in NVLink
    adapter.
  - A caller sensor passed to `WithCollect` that produces NVLink signals must
    name them, or the run fails because the built-in sources produce them too.
- The collect adapter name `gpu` is reserved for the built-in GPU adapter.
  Drop a caller-registered adapter of that name: the built-in one reads the
  canonical GPU signals from whichever registered sensor produces them.
- `App.WithCollect` requires the signal IDs the sensor supplies: a bare
  `WithCollect(sensor)` is a registration error. Pass the IDs, for example
  `WithCollect(sensor, cpusignals.OnlineCount.ID(), cpusignals.PossibleCount.ID())`.
  Claims are explicit so a release that adds built-in collect sources never
  changes which sensor supplies a signal. Related registration rules, all checked whatever checks
  are runnable: one signal claimed by two sensors is an error, every catalog
  category name (`cpu`, `nvlink`, …) is reserved for built-in adapters, and a
  caller adapter may not feed a catalog field that an active built-in adapter
  of its category also feeds. The stock CLI is unaffected.
- `run` records every validated threshold override in the host's systemd
  journal before collecting and exits `2` if it cannot. Container deployments
  that pass overrides must mount `/run/systemd/journal/socket`; see
  `docs/integration.md`. The new `Catalog.ConfiguredOverrides` returns the
  overrides a catalog is configured with, resolved as evaluation would. ([#93](https://github.com/dsx-ai-factory/health-validation/pull/93))
- `CheckDefinition.Validate`, and so `Catalog.Register` and `MustRegister`,
  rejects a check that names no `RequiredSignals`, or a blank one. A check
  with none was routed every measurement of its category and ran its rules on
  zero values when collection failed. Name the signals the check's rules
  read. A custom check
  passed to the CLI through `App.WithChecks` without them now stops the run
  with exit 2 once GPU architecture discovery succeeds, before the sensors
  are collected. If discovery fails, `run` still reports the
  `architecture_discovery` verdict and exits 1.
- Domains the CLI collects through `collect` sensors and the toolkit's collect
  bridge report the same severities, failed signals, and recommended actions
  as the legacy toolkit sensors, except as listed below. Collection-failure
  messages now name the collect source and operation, for example
  `cpu.sysfs operation "read /sys/devices/system/cpu/online" failed with …`.
  - CPU and NUMA: only the collection-failure wording changes. ([#135](https://github.com/dsx-ai-factory/health-validation/pull/135))
  - Xid: ([#179](https://github.com/dsx-ai-factory/health-validation/pull/179))
    - An Xid line with a device address that cannot be parsed fails every Xid
      check with `check_collection`; the legacy sensor dropped it and passed.
    - A boot with no readable kernel entries fails the Xid checks.
    - MIG-attributed lines (`GPU-I:`/`GPU-CI:`) and addresses without the
      `PCI:` token now count, so their checks can fail where they passed.
    - Kernel lines that contain `NVRM: Xid ` without a device address are
      ignored.
    - `name=<unknown>` with a numeric pid gives an empty process name, an
      overflowing pid is absent, and NVLink5 process names are trimmed.
    - An Xid entry stamped in the future after a clock step fails the Xid
      checks until the clock passes it.
    - `WithCollect` must claim all Xid signals or none, and the collect adapter
      name `xid` is reserved.
  - Xid per GPU: each Xid check gives one result per GPU and names it in
    `affected_entities`, and `occurrences` counts per GPU. The defaults are 1,
    so this matters only with an override. ([#231](https://github.com/dsx-ai-factory/health-validation/pull/231))
  - Node GPU count and architecture discovery: a driver that hangs during
    discovery ends in a discovery-failure verdict after the collection timeout
    instead of hanging the run. ([#195](https://github.com/dsx-ai-factory/health-validation/pull/195))
  - GPU, beyond the DCGM entry above: ([#262](https://github.com/dsx-ai-factory/health-validation/pull/262))
    - A GPU whose UUID cannot be read is named by its source-local slot in
      `affected_entities`.
    - A process that exits between the NVML and procfs reads is left out of
      `compute_process_cmdlines`.
    - When every GPU source times out, each source gives its own
      `retry_collection` result per check. When one source stalls, only the
      checks it feeds fail.
    - Power comes from DCGM in watts, so `observed_values` can differ from
      NVML's by a fraction of a watt.
  - RDMA link flap: ([#236](https://github.com/dsx-ai-factory/health-validation/pull/236))
    - A user who can read only their own journal entries, not the kernel's,
      now gets UNKNOWN with `check_collection`. The legacy probe found the
      user's entries and passed, so non-root runs, such as Slurm jobs, go from
      exit 0 to exit 1.
    - A line that looks like an mlx5 link transition but does not parse fails
      `rdma_linkflap_fatal` with `check_collection`; the legacy sensor skipped
      it.
    - A transition counts only when the message starts with `mlx5_core`, so
      another subsystem's line that mentions the driver no longer counts.
  - Across domains:
    - Per-GPU results are ordered by GPU identity rather than NVML index.
    - Architecture discovery that finds NVML busy with an earlier call in the
      same process reports UNKNOWN with `retry_collection` instead of CRITICAL
      with the reset, restart, and escalate sequence.
    - The CLI needs access to every GPU on the node, so that DCGM and NVML
      report the same GPUs.
    - `App.Run` no longer closes the host's DCGM and NVML connections; call
      `App.Close` when done with the App.

- The legacy toolkit sensors are removed. The CLI has collected every
  built-in domain through `collect` sensors since the changes above, so the
  stock CLI is unaffected. Library callers migrate as follows:
  - `toolkit.Collect` and `CollectWithOptions` still take `toolkit.Sensor`,
    and a `collect.Sensor` does not satisfy it, so swapping constructors does
    not compile. Wrap the collect sensor in a `toolkit.Sensor` whose `Measure`
    converts its batch with `observations.ToMeasurements` and whose `Errored`
    returns the domain's `ErroredXxx` measurement. `Example_collectSensor` in
    `toolkit` does this for the CPU sensor. Return an error when the
    conversion yields no measurements, or the domain's checks are skipped
    instead of failed. Convert all of a domain's observations in one call: GPU
    and NVLink read both DCGM and NVML, and a measurement built from one
    source reports the other source's signals as failed. Give each signal one
    source: both sensors produce power draw and the power limits, and a signal
    fed twice is also reported failed. Pass `collect.WithSignalID` to each
    sensor's `Measure` with disjoint sets. The CLI reads power draw, power
    limits, performance state, the hardware slowdown signals, and fabric
    registration from DCGM, and the rest from NVML.
    `agentkit/examples/scan-one-off-evaluate` shows opening and closing a DCGM
    source for the DCGM sensor.
  - `App.WithCPUBackend`, `WithGPUBackend`, `WithNodeBackend`,
    `WithNVLinkBackend`, `WithXidBackend`, and `WithRDMALinkFlapBackend`:
    pass a `collect.Sensor` and the signal IDs it supplies to
    `App.WithCollect`, for example
    `WithCollect(sensor, observations.MappedSignalsFor(health.CPU)...)`. Claim
    only signals a registered adapter reads: `cpusignals.All()` also holds
    activity signals no adapter reads, and claiming one fails registration.
    GPU and NVLink map signals from both DCGM and NVML, so a sensor for one of
    them claims only the mapped signals its own `Signals()` lists.
    The claimed signals replace the built-in source's; the built-in adapter
    still builds the domain's measurements.
  - `CPUBackend`, `CPUSensor`, `NewCPUSensor`, and `SysfsCPU`: use
    `collect/sensors/cpu`.
  - `GPUBackend`, `GPUSensor`, `NewGPUSensor`, `NodeBackend`, `NodeSensor`,
    `NewNodeSensor`, `NVLinkBackend`, `NVLinkSensor`, `NewNVLinkSensor`,
    `MultiBackend`, `LazyGPUBackend`, and the `toolkit/nvml` package
    (`NVMLBackend`, `New`, `NewWithLibrary`, `DeviceProber`, `ProbeContext`,
    `ProbeDevice`, `Error`): use `collect/sensors/nvml` and
    `collect/sensors/dcgm`.
  - `App.NVMLBackend` and its `WithGPUProbe` and `WithNVLinkProbe` probes:
    for a per-GPU check of your own, read the signal through a `GPUAdapter`
    (`docs/extending.md`); a signal the built-in GPU sources do not produce
    needs a collect sensor registered with `WithCollect`. There is no
    per-link replacement for `WithNVLinkProbe`: a per-link signal needs your
    own collect sensor, adapter, and checks (`WithCollect`,
    `WithCollectAdapters`, `WithChecks`).
  - `toolkit.Collect` no longer skips a sensor whose `ResourceKey` matches one
    that already timed out. Only the removed GPU, node, and NVLink sensors
    shared a backend that way; each sensor now gets its own timeout.
  - `XidBackend`, `XidSensor`, `NewXidSensor`, and `Journalctl`: use
    `collect/sensors/xid`.
  - `RDMALinkFlapBackend`, `RDMALinkFlapSensor`, `NewRDMALinkFlapSensor`,
    `JournalctlRDMALinkFlap`, `RDMALinkFlapOption`, `WithLinkFlapLookback`, and
    `WithLinkFlapBootOffset`: use `collect/sensors/linkflap` with its
    `WithLookback` and `WithBootOffset` options.
  - `IBBackend`, `IBSensor`, `NewIBSensor`, and `App.WithIBBackend`, the opt-in
    generic InfiniBand sensor the CLI never wired: register your own collect
    sensor with `WithCollect` and its adapter with `WithCollectAdapters`.
  - `MockCPU`, `MockGPU`, `MockNode`, `MockNVLink`, `MockXid`, and
    `MockRDMALinkFlap`: to test without hardware, pass any `toolkit.Sensor`
    that returns fixed measurements to `toolkit.Collect`, or claim signals from
    an in-memory collect sensor with `WithCollect` (`docs/faq.md`).

### Features

- `app.GPUAdapter` builds one measurement of your own type per GPU from
  collect signals, for checks added with `WithChecks`, leaving the built-in GPU
  domain in place. `docs/extending.md` describes the wiring.
- The built-in NVML source (`gpu.nvml`), which produced only the
  compute-process signals, now produces every signal collect's NVML sensor
  declares
  ([list](https://github.com/dsx-ai-factory/health-validation/blob/cc26732a/collect/sensors/nvml/sensor.go#L63-L69):
  temperature, power, ECC and row remapping, NVLink and fabric, memory,
  utilization, clocks, and processes) except the seven read from DCGM:
  `gpu.power.draw`, `gpu.power.limit.requested`, `gpu.power.limit.enforced`,
  `gpu.performance.state`, `gpu.clock.hardware_slowdown.active`,
  `gpu.clock.hardware_thermal_slowdown.active`, and
  `gpu.clock.hardware_power_brake_slowdown.active`. A signal such as ECC or
  row remapping therefore needs only a `GPUAdapter`. A `WithCollect` claim on any of these signals still takes
  precedence over the built-in source.
- `health.KeyOf` treats a zero `EntityKey` as unset and keys the measurement
  by its `EntityID`, so a measurement embedding `observations.Entity` that a
  test constructs directly names its GPU.

### Fixes

- Collect sources log a warning to stderr when they time out or fail as a
  whole, including failures a sensor reports inside its batch, as the legacy
  sensors did. Each line names the collect sensor (`sensor`) and the failing
  library or reader (`source`); the legacy lines named the toolkit sensor.
  Any failed read of a node-wide sensor, such as the CPU NUMA count, also
  warns. Errors on individual GPUs are not logged.

## [catalog/v0.3.0] and [toolkit/v0.3.0] - 2026-10-05

Covers everything since `catalog/v0.2.0` and `toolkit/v0.2.0`. Build, CI,
test, and documentation-only changes are omitted.

### Breaking Changes

- The verdict `schema_version` is now `0.3`, and three wire values change.
  Consumers matching on the old strings must update.
  - The `operational_state` value `none` is now `ready`, because `none` read
    as "no state" rather than "the node stays schedulable"
    (`health.OperationalNone` → `health.OperationalReady`). ([#103](https://github.com/dsx-ai-factory/health-validation/pull/103))
  - The `contact_support` remediation action is now `service_required`
    (`RemediationActionContactSupport` → `RemediationActionServiceRequired`). ([#104](https://github.com/dsx-ai-factory/health-validation/pull/104))
  - The `workflow_nvlink5_err` remediation action is removed. Fatal Xid
    144-150 occurrences now recommend `reset_gpu` then `service_required`. ([#52](https://github.com/dsx-ai-factory/health-validation/pull/52))
- `toolkit.CollectWithTimeout` is replaced by `toolkit.CollectWithOptions`,
  which takes a `CollectionOptions` carrying the timeout and an optional
  scheduling cadence. ([#84](https://github.com/dsx-ai-factory/health-validation/pull/84))
- `toolkit.ParseXid` moves to the new `toolkit/xid` package. ([#52](https://github.com/dsx-ai-factory/health-validation/pull/52))

### Features

- Optional `--cadence CONTINUOUS|PROLOG|MAINTENANCE` on the CLI, and a
  matching cadence in `catalog.EvaluationOptions` / `toolkit.CollectionOptions`,
  run only the checks whose scheduling cadence covers that window. Checks
  outside it are reported as `CADENCE_MISMATCH` skips. Omitting the cadence
  still evaluates every registered check. ([#84](https://github.com/dsx-ai-factory/health-validation/pull/84))
- New `gpu_compute_apps` check: CRITICAL when a compute (CUDA) process still
  holds a GPU, with each process's command line in the diagnostic, so a
  leftover job cannot look like a clean node. It runs only in the guest,
  where those processes are visible. ([#54](https://github.com/dsx-ai-factory/health-validation/pull/54))
- Xid 144-150 (the NVLink5/NVSwitch fault family) follow the Fatal/Nonfatal
  flag NVRM logs with each occurrence: Fatal is CRITICAL, Nonfatal is
  WARNING with no action. These codes previously fired CRITICAL on any
  occurrence. The parser also handles the extended log shapes real NVRM
  output uses for this family. ([#52](https://github.com/dsx-ai-factory/health-validation/pull/52))
- Application-level Xids and `gpu_xid_warning` now recommend `restart_app`
  then `service_required`, so a fault that persists past the restart sends
  the node for service. ([#102](https://github.com/dsx-ai-factory/health-validation/pull/102))
- The CLI can push each verdict to an OTLP/HTTP metrics endpoint, configured
  under `metrics:` in the config file. Publishing runs after the verdict is
  written and never changes the exit code. See
  [Publishing metrics](README.md#publishing-metrics). ([#90](https://github.com/dsx-ai-factory/health-validation/pull/90))
- A measurement can expose signals it computes through the optional
  `health.DerivedSignaler` interface, so values that depend on catalog
  knowledge can stay in the catalog. ([#83](https://github.com/dsx-ai-factory/health-validation/pull/83))
- The in-repo `collect` module gains DCGM signals for the GPU and NVLink
  checks, typed observation targets, and an explicit representation for
  absent values. It is still not wired into the CLI or the container
  image. ([#60](https://github.com/dsx-ai-factory/health-validation/pull/60), [#96](https://github.com/dsx-ai-factory/health-validation/pull/96))

### Fixes

- Evaluating a pointer to a measurement no longer panics, and a json tag
  carrying only options (such as `json:",omitempty"`) now yields the Go
  field name as its signal name instead of an empty one. ([#83](https://github.com/dsx-ai-factory/health-validation/pull/83))

## [catalog/v0.2.0] and [toolkit/v0.2.0] - 2026-09-17

Covers everything since `catalog/v0.1.0` and `toolkit/v0.1.0`. Build, CI,
and documentation-only changes are omitted.

### Breaking Changes

- The verdict JSON no longer carries a single recommended action. Each
  check now carries `remediation_actions`: an ordered sequence of steps to
  work through, re-running the whole suite between steps. `RecommendedAction.DiagnosticAction`
  is replaced by `RecommendedAction.RemediationActions`. See
  [`docs/remediation.md`](docs/remediation.md) for the consumption contract. ([#45](https://github.com/dsx-ai-factory/health-validation/pull/45))
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
  see the [container journal access profiles](docs/integration.md#security-context-and-check-coverage). ([#15](https://github.com/dsx-ai-factory/health-validation/pull/15))

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

[Unreleased]: https://github.com/dsx-ai-factory/health-validation/compare/toolkit/v0.3.1...main
[catalog/v0.3.0]: https://github.com/dsx-ai-factory/health-validation/tree/catalog/v0.3.0
[toolkit/v0.3.0]: https://github.com/dsx-ai-factory/health-validation/tree/toolkit/v0.3.0
[catalog/v0.2.0]: https://github.com/dsx-ai-factory/health-validation/tree/catalog/v0.2.0
[toolkit/v0.2.0]: https://github.com/dsx-ai-factory/health-validation/tree/toolkit/v0.2.0
[catalog/v0.1.0]: https://github.com/dsx-ai-factory/health-validation/tree/catalog/v0.1.0
[toolkit/v0.1.0]: https://github.com/dsx-ai-factory/health-validation/tree/toolkit/v0.1.0
