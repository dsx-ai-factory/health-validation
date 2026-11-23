# Changelog

All notable changes to NVIDIA Health Validation are documented here.
This project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Breaking Changes

- The `gpu_power_state` check is renamed `gpu_performance_state`, and
  `gpu.GPUPowerState` is now `gpu.GPUPerformanceState`, because the check
  reads the GPU's performance state (P-state), not a power state. Its
  thresholds are renamed to match the range it enforces, in NVML's terms (P0 is
  maximum performance): `expected_gpu_pstate` is now `max_performance_pstate`,
  and the unreleased `idle_gpu_pstate` is now `min_performance_pstate`. Update `checks:` overrides and `disabled_checks` entries
  in cluster config, and any consumer that matches the check ID in a verdict. ([#342](https://github.com/dsx-ai-factory/health-validation/pull/342))
- `CheckDefinition.Validate`, and so `Register`, rejects rules below `UNKNOWN`
  that recommend `drain`. Recommend `monitor` for them instead; any severity
  may. The verdict `schema_version` is now `0.7` (`0.3` in v0.3.x). ([#209](https://github.com/dsx-ai-factory/health-validation/issues/209), [#227](https://github.com/dsx-ai-factory/health-validation/issues/227))
- Schema `0.6` changed `affected_components` to list each
  failing check once, under `checks`, with that check's `entities`. The
  parallel `check_ids` and `entities` arrays are removed.
  ([#210](https://github.com/dsx-ai-factory/health-validation/issues/210))
- `CRITICAL` now ranks above `UNKNOWN` in the verdict (schema `0.5`), and the
  Go `Critical` and `Unknown` constants in `catalog/health` swap numeric values.
  Compare severities by name, not by number. ([#267](https://github.com/dsx-ai-factory/health-validation/pull/267))
- The `catalog`, `toolkit`, `collect`, and `agentkit` modules are merged into
  the single root module `github.com/dsx-ai-factory/health-validation`, with one
  `go.mod` and one version. Import paths are unchanged. Replace requires on
  `…/catalog`, `…/toolkit`, `…/collect`, and `…/agentkit` with one require on the root module
  at the first `vX.Y.Z` release. Releases are tagged `vX.Y.Z` instead of
  `catalog/vX.Y.Z` and `toolkit/vX.Y.Z`, and the verdict's `catalog_version`
  equals the CLI version. ([#216](https://github.com/dsx-ai-factory/health-validation/pull/216))
- The public `catalog/version` package is removed. Read the version from a
  verdict's `catalog_version`. In unstamped builds, `health-validation version`
  now reports the module version Go embeds, or `dev+<commit>`, instead of `dev`. ([#223](https://github.com/dsx-ai-factory/health-validation/pull/223))
- `run` records every validated threshold override in the host's systemd
  journal before collecting and exits `2` if it cannot. Container deployments
  that pass overrides must mount `/run/systemd/journal/socket`; see
  `docs/integration.md`. The new `Catalog.ConfiguredOverrides` returns the
  overrides a catalog is configured with, resolved as evaluation would. ([#93](https://github.com/dsx-ai-factory/health-validation/pull/93))
- `CheckDefinition.Validate`, and so `Catalog.Register` and `MustRegister`,
  rejects a check that names no `RequiredSignals`, or a blank one. A check
  with none was routed every measurement of its category and ran its rules on
  zero values when collection failed ([#263](https://github.com/dsx-ai-factory/health-validation/pull/263)). Name the signals the check's rules
  read. A custom check
  passed to the CLI through `App.WithChecks` without them now stops the run
  with exit 2 once GPU architecture discovery succeeds, before the sensors
  are collected. If discovery fails, `run` still reports the
  `architecture_discovery` verdict and exits 1.
- The remediation action constants `RemediationActionCheckUVM` (`check_uvm`),
  `RemediationActionWorkflowXid45` (`workflow_xid_45`), and
  `RemediationActionXid154` (`xid_154`) are removed, and nothing recommends
  those actions any longer. Drop references to them.
  ([#138](https://github.com/dsx-ai-factory/health-validation/pull/138), [#230](https://github.com/dsx-ai-factory/health-validation/pull/230), [#78](https://github.com/dsx-ai-factory/health-validation/pull/78))
- The `DEPENDENCY_SKIPPED` skip reason is removed from the verdict schema and
  from `health.SkipReason`, so remove any match on it.
  ([#91](https://github.com/dsx-ai-factory/health-validation/pull/91))
- Every `ThresholdSpec` must declare a `ValueType`; registration rejects one
  that does not, so out-of-tree checks must add it. `Register` coerces the
  default, per-architecture values, and overrides to the declared type, and
  rejects NaN, infinities, and lossy conversions, with the offending check and
  parameter named in the error. A string where a number belongs is now a
  configuration error instead of a check that fails on every GPU. Optional
  `ValueMin`/`ValueMax` bounds are enforced on defaults, per-architecture
  values, and overrides alike, and an `AdjustableThreshold` must declare at
  least one. The 100 kW ceilings on `expected_power_limit_w` and `min_power_w`
  are gone.
  ([#166](https://github.com/dsx-ai-factory/health-validation/pull/166))
- The evaluation engine rejects two measurements that feed one check for the
  same entity with `ErrAmbiguousMeasurement`, and the CLI exits `2`. A
  measurement type without `EntityIdentifier` is keyed as the node, so two
  instances collide. Give a per-entity measurement an `EntityID`.
  ([#197](https://github.com/dsx-ai-factory/health-validation/pull/197))
- Public API hardening. ([#175](https://github.com/dsx-ai-factory/health-validation/pull/175))
  - Removed `NodeVerdict.ExitCode` (its 0/1/2/3 scheme differed from the CLI's;
    read `OverallSeverity`), `CheckDefinition.FeasibilityClass`, and
    `ResolveThresholds` (now internal).
  - `MeasurementSignals` is a function returning a copy in every domain
    package.

### Changes

Changes to behavior and APIs outside the
[compatibility guarantees](RELEASE.md#compatibility).

- `gpu_compute_apps` also runs under `OPERATOR_INFRA`, for operators who run
  it on the host before and after their own jobs. Where jobs run in a tenant
  VM, keep running it in that guest: the host cannot see the VM's processes.
  ([#352](https://github.com/dsx-ai-factory/health-validation/pull/352))
- Monitor recommendations can carry remediation steps and appear in the
  node-level summary. Consumers must inspect `operational_state` before treating
  a sequence as a drain workflow; application actions still require context
  from `check_results`. `WARNING` and `INFO` results no longer recommend
  `drain`. The warning-class Xid checks (including `gpu_xid_45` alone) now
  recommend `monitor` with `["restart_app"]`, and `gpu_xid_78` recommends
  `monitor` with `["update_swfw"]`. Xid 154 Drain P2P is CRITICAL and retains
  its drain sequence because it requires disabling scheduling and stopping P2P
  traffic. `remediation_actions` is now emitted with `monitor` as well as
  `drain`, and the node-level `recommended_action` can be a `monitor` sequence
  when no eligible drain sequence exists. Drain sequences outrank monitor
  sequences, then severity and action rank decide. Consumers that drain on
  `operational_state: drain` stop draining for these findings. CRITICAL and
  UNKNOWN still exit `1`. Consumers determine repair scope from the check
  results and affected entities. The new `health.MonitorWith` builds a
  `monitor` recommendation with steps. ([#209](https://github.com/dsx-ai-factory/health-validation/issues/209), [#227](https://github.com/dsx-ai-factory/health-validation/issues/227))
- Collection failures report `UNKNOWN` instead of `CRITICAL`. Update alerts
  that match collection failures as `CRITICAL` to handle `UNKNOWN`.
  ([#267](https://github.com/dsx-ai-factory/health-validation/pull/267))
- Xid severity and remediation changes. Downstream remediation follows the
  recommended sequences as published, so these change behavior without breaking
  consumers:
  - Xid 159 recommends a GPU reset instead of `check_uvm`.
    ([#138](https://github.com/dsx-ai-factory/health-validation/pull/138))
  - Xid 151 (key rotation error) is now CRITICAL instead of WARNING.
    ([#168](https://github.com/dsx-ai-factory/health-validation/pull/168))
  - Xid 45 (preemptive removal) is WARNING with `restart_app` when it is the
    only Xid seen in the evaluated window (the whole current boot by default),
    and INFO with no action when another Xid is present in it (Xid 154 does
    not count).
    ([#230](https://github.com/dsx-ai-factory/health-validation/pull/230))
  - Remediation sequences follow NVIDIA's catalog: Xids 79 and 151 recommend
    `restart_os` then `service_required`, the application-level warning
    classes recommend `restart_app` alone rather than followed by
    `service_required`, and the Xid 78 and Xid 151 messages say what to fix.
    ([#234](https://github.com/dsx-ai-factory/health-validation/pull/234))
  - `gpu_xid_154` reports the recovery action the driver last requested for each
    GPU instead of an INFO notice on every Xid 154. That holds when a GPU's
    readable reports carry distinct sequence numbers. When they cannot be
    ordered, the GPU keeps the most disruptive action it reported and each
    report counts under its own action, so an earlier, less severe request can
    still reach `occurrences` and affect the verdict. OS reboot, bus reset, and
    system reboot are CRITICAL with `restart_os` then `service_required`. GPU
    reset is CRITICAL with `reset_gpu` then `service_required`. Drain and Reset
    is CRITICAL with the same sequence. Drain P2P is CRITICAL with the new
    `stop_p2p_traffic` action, then `reset_gpu`, then `service_required`. An
    action the catalog does not map, or a transition that cannot be read, is
    WARNING with `monitor`. `None` is INFO.
    ([#78](https://github.com/dsx-ai-factory/health-validation/pull/78))
- A check's `Dependencies` no longer skip it. A dependent check always runs
  and reports its own result. Only when its entity could not be read at all and
  a dependency is Critical or Unknown does its collection-failure message name
  the dependency as the cause. `gpu_count` is now the declared cause of the GPU
  and NVLink checks' collection failures on GB200/GB300.
  ([#91](https://github.com/dsx-ai-factory/health-validation/pull/91))
- The CLI reads the GPU domain from DCGM, through a hostengine running on the
  node, and from NVML only for compute processes. Nodes need `nv-hostengine`
  and the DCGM 4 client library (`libdcgm.so.4`); without them the GPU power,
  power-limit, pstate, and throttle checks report a collection failure. There
  is no fallback to NVML. ([#262](https://github.com/dsx-ai-factory/health-validation/pull/262))
- The CLI reads the NVLink domain through collect sensors: fabric
  registration from DCGM, and link state, count, speed, capabilities, P2P,
  and fabric health from NVML. Without a reachable hostengine, `nvlink_fabric`
  reports a collection failure. Other differences from the legacy NVLink
  sensor, from the conversion added in
  [#237](https://github.com/dsx-ai-factory/health-validation/pull/237) and the switch in [#275](https://github.com/dsx-ai-factory/health-validation/pull/275):
  - A driver reporting `NOT_SUPPORTED` past a GPU's last link no longer fails
    `nvlink_capabilities`, and a disabled link past the reported count no
    longer fails `nvlink_active`: links past the count are not read.
  - An unknown fabric health value fails `nvlink_fabric_health` with
    `check_collection` instead of passing.
  - Through the library, a node with no GPUs fails every NVLink check with
    `check_collection` instead of skipping it. The CLI is unchanged: it stops
    at `architecture_discovery` before running any check.
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
    ([#289](https://github.com/dsx-ai-factory/health-validation/pull/289))
- Also in [#175](https://github.com/dsx-ai-factory/health-validation/pull/175): `MockIB` is removed, the Xid parser
  (`toolkit/xid`) and `ParseLinkFlaps` are internal, and the `Errored*` helpers
  default to every signal when given none. None had known consumers.
- InfiniBand collection options ([#201](https://github.com/dsx-ai-factory/health-validation/pull/201)).
  `collect/source/infiniband.New(files, excludedDevices ...string)` is now
  `New(files, opts ...Option)`. Replace `New(files, names...)` with
  `New(files, WithExcludedDevices(names...))`; a `[]string` spread into the
  options no longer compiles. `WithVendors` selects devices by the PCI vendor ID
  in `<device>/device/vendor` (`VendorNVIDIA` is `0x15b3`) instead of by name.
  The defaults are unchanged: mlx-named devices, matching GPUd.
  `collect/sensors/infiniband.New` takes options too, and `WithLinkLayers`
  admits non-InfiniBand (RoCE) ports; the default stays InfiniBand-only.
- `toolkit/observations` converts several measurement types per category when
  their signals are disjoint, and carries InfiniBand port and device targets in
  both directions. The collect bridge validates those targets.
  `infiniband.PortMeasurement` implements `health.EntityKeyed`: a named port is
  verified under `<device>/<port>`, and a failure before any port was
  enumerated is keyed by its source. ([#201](https://github.com/dsx-ai-factory/health-validation/pull/201))

### Features

- `serve` exposes a liveness signal for stuck native calls: `GET /healthz` and
  the standard gRPC health service (`grpc.health.v1.Health`, empty service name)
  report unhealthy once a source call has run longer than the new
  `--stuck-call-deadline` (default `10m`, must exceed `--request-timeout`), so a
  supervisor can restart the process. Probes bypass the in-flight request limit.
  This is liveness only; readiness is still not provided. ([#326](https://github.com/dsx-ai-factory/health-validation/pull/326), [#319](https://github.com/dsx-ai-factory/health-validation/issues/319))
- Add opt-in passive DCGM health collection through `dcgm.NewWithHealth`, with
  an explicit selection of nine health systems, optional metric signals,
  and one `dcgm.health` report. A zero systems mask skips health collection;
  omit metric signals to request only health when the mask is nonzero.
  Reports preserve all native incidents, group metadata, readable health statuses,
  monitoring start time, and missing-target facts. Entities reuse shared GPU
  and NVSwitch references with optional source-native identity metadata.
  Each group's `entity_results` keeps identities, verified group membership,
  incidents, and scoped collection errors together. GPU references include
  available UUIDs and PCI bus addresses from one lookup.
  Each GPU has its own native health group; NVSwitches share a separate group
  on the same session, with
  independent check errors, native overall health, start times, truncation flags,
  and entity results in `group_results`. Native group handles stay internal.
  A later group failure preserves completed results.
  Unknown native entity kinds are preserved. Existing metric-only constructors
  and defaults are unchanged.
  Source `HealthWatch` options configure the health update interval and retention
  independently of metric `WatchPolicy`, keeping 30 seconds and 10 minutes as
  defaults.
  Health reads discover GPUs once per collection and reuse their groups while
  present. Missing GPUs' groups are queued for cleanup before new groups are allocated;
  failed cleanup is retained for retry. Discovery failures preserve known groups.
  Rejected GPU or NVSwitch additions preserve findings from usable members,
  record errors under the affected entity result, and retry on the next read.
  Unattributed discovery failures appear in `system_errors`.
  Incidents include native error severity, category, and suggestions when the
  DCGM client library provides them.
  Source `Collect` keeps independently selected metrics, health, and inventory
  on one client session while allowing concurrent collections to interleave.
  Recovery replaces failed sessions only for fresh collections; existing
  collections preserve completed results without using replacement identities.
  Driver health monitors GPU recovery actions reported by the driver.
  Successful native reports are available immediately, including empty incident
  lists; absent incidents do not establish healthy status or complete coverage.
  Consumers choose any startup grace period.
  Cleanup destroys the owned group and releases the client on a best-effort
  basis; native health sampling may persist in an external hostengine.
  Native health watches share field sampling with metric watches and can
  request faster updates than a slower metric interval policy.
  ([#298](https://github.com/dsx-ai-factory/health-validation/pull/298))

- Experimental checks (`maturity_level: experimental`) are off by default. One
  that is not enabled is skipped with `skip_reason: CHECK_DISABLED` and a
  `skip_detail` naming the cause, so it never affects `overall_severity` or the
  exit code and nothing is collected for it. The config enables them with
  `experimental.enabled: true` (all of them, including ones a later release
  adds) or by ID under `experimental.checks`, validated like `disabled_checks`;
  `disabled_checks` still wins. Library consumers use the new
  `catalog.WithAllExperimentalChecks` and `catalog.WithExperimentalChecks`.
  `ib_port_state`, the only experimental check so far, is now off unless
  enabled. A check you register with `MaturityLevel: Experimental` is gated the
  same way, so enable it to keep it running. ([#323](https://github.com/dsx-ai-factory/health-validation/pull/323))
- `app.GPUAdapter` builds one measurement of your own type per GPU from
  collect signals, for checks added with `WithChecks`, leaving the built-in GPU
  domain in place. `docs/extending.md` describes the wiring.
- `WithCollect` and `WithCollectAdapters` register collect sensors and adapters
  with the app. They have not been in a release, so these registration rules
  are not breaking changes:
  - `WithCollect(sensor, signalIDs...)` requires the signal IDs the sensor
    supplies: a bare `WithCollect(sensor)` is a registration error. Pass the
    IDs, for example
    `WithCollect(sensor, cpusignals.OnlineCount.ID(), cpusignals.PossibleCount.ID())`.
    Claims are explicit so a release that adds built-in collect sources never
    changes which sensor supplies a signal. One signal claimed by two sensors is
    an error, whatever checks are runnable, and a sensor that produces NVLink
    signals must name them. The stock CLI is unaffected.
    ([#134](https://github.com/dsx-ai-factory/health-validation/pull/134), [#273](https://github.com/dsx-ai-factory/health-validation/pull/273), [#275](https://github.com/dsx-ai-factory/health-validation/pull/275))
  - Every catalog category name (`cpu`, `nvlink`, …) is reserved as an adapter
    name for the built-in adapter, and a caller adapter may not feed a catalog
    field that an active built-in adapter of its category also feeds. Drop a
    caller adapter named `gpu`: the built-in one reads the GPU signals from
    whichever registered sensor produces them.
    ([#262](https://github.com/dsx-ai-factory/health-validation/pull/262), [#273](https://github.com/dsx-ai-factory/health-validation/pull/273), [#275](https://github.com/dsx-ai-factory/health-validation/pull/275))
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
- `--execution-context OPERATOR_INFRA|TENANT_GUEST|OUT_OF_BAND` selects the
  execution context the CLI evaluates in, and `health.ExecutionContextFromName`
  parses the names. The default is `TENANT_GUEST`, so `gpu_compute_apps`, which
  runs only in the guest, now runs by default; pass `--execution-context
  OPERATOR_INFRA` to skip it as before.
  ([#139](https://github.com/dsx-ai-factory/health-validation/pull/139))
- `health-validation serve` keeps the node's collectors available and evaluates
  on request, over HTTP (`GET` and `POST /verdict`) and gRPC
  (`health.verdict.v1.VerdictService/Evaluate`) on one listener. Each request
  runs a fresh collection cycle through the same evaluation core as `run`, and
  may override `node_id`, `cadence`, `execution_context`, `xid_from`, and
  `xid_to`. The listener accepts only a numeric loopback address (default
  `127.0.0.1:8080`) and has no TLS or authentication. The verdict service and its
  protobuf API (`proto/health/v1`) are experimental, like `agentkit`. See
  `docs/agent-mode.md`. `App.RunAndClose(args, stdout, stderr)` runs the CLI and
  closes the host backend after a one-shot command; `serve` manages its own
  shutdown. ([#268](https://github.com/dsx-ai-factory/health-validation/pull/268))
- New `ib_port_state` check: every InfiniBand port, RoCE included, must be
  ACTIVE with its physical link up. It evaluates the new
  `infiniband.PortMeasurement`, one per sysfs port, and names the failing port
  (`mlx5_0/1`) in `affected_components[].checks[].entities`.
  `excluded_link_layers` (empty by default) leaves a link layer out, such as
  Ethernet for uncabled RoCE ports; because it excludes, a misspelled entry
  checks more ports, never fewer. It runs on GB200 and GB300. Finding no ports
  at all is a collection failure, not a skip, so a deployment without
  InfiniBand disables the check. The stock CLI reads it through the
  `collect/sensors/infiniband` sysfs sensor on the collect bridge, requesting
  port signals only when a port check will run. It selects devices by PCI
  vendor (`WithVendors(VendorNVIDIA)`), so a DOWN port on a renamed NVIDIA HCA
  such as `rdma0` is reported instead of hidden behind a healthy `mlx5_0`, while
  other vendors' and software RDMA devices are not checked.
  ([#201](https://github.com/dsx-ai-factory/health-validation/pull/201))
- Node-level `recommended_action`: `NodeVerdict` carries the one complete
  remediation sequence that outranks the others. A `drain` sequence outranks a
  `monitor` sequence. Otherwise severity, then each step's rank in sequence
  order, then check ID decide. Consumers must inspect `operational_state`
  before treating the sequence as a drain workflow. Absent means no remediation
  sequence is available, not that the node is healthy. See
  `docs/remediation.md`.
  ([#248](https://github.com/dsx-ai-factory/health-validation/pull/248))
- New architectures. Each is added to the checks that apply to it, with its
  own thresholds where they differ.
  - B200 ([#225](https://github.com/dsx-ai-factory/health-validation/pull/225)) and B300 ([#296](https://github.com/dsx-ai-factory/health-validation/pull/296)), the HGX parts. B300 is
    recognized by its product name and shares B200's Xid scope.
  - VR: `gpu_power_limit` ([#239](https://github.com/dsx-ai-factory/health-validation/pull/239)), `gpu_count` ([#240](https://github.com/dsx-ai-factory/health-validation/pull/240)),
    `gpu_sensor_throttle` ([#243](https://github.com/dsx-ai-factory/health-validation/pull/243)), the NVLink active and
    count/speed checks ([#244](https://github.com/dsx-ai-factory/health-validation/pull/244)), `rdma_linkflap_fatal`
    ([#245](https://github.com/dsx-ai-factory/health-validation/pull/245)), `numa_node_count` ([#310](https://github.com/dsx-ai-factory/health-validation/pull/310)), `gpu_compute_apps`
    ([#313](https://github.com/dsx-ai-factory/health-validation/pull/313)), and `nvlink_capabilities`, `nvlink_fabric`, and
    `nvlink_fabric_health` ([#325](https://github.com/dsx-ai-factory/health-validation/pull/325)). Discovery recognizes any GPU whose name contains `VR`
    ([#238](https://github.com/dsx-ai-factory/health-validation/pull/238), [#288](https://github.com/dsx-ai-factory/health-validation/pull/288)). A GPU that reports a generic name stays
    `unknown`.
  - VR on `gpu_performance_state`: a GPU now passes anywhere from `max_performance_pstate`
    to the new `min_performance_pstate`, which is assumed to be the same or higher.
    VR idles at P5, so it passes P0 to P5. Other GPUs keep P0 for both, so
    they still pass only at P0. ([#327](https://github.com/dsx-ai-factory/health-validation/pull/327))
  - The Xid checks cover H100, H200, and A100. ([#194](https://github.com/dsx-ai-factory/health-validation/pull/194))
- Xid additions:
  - Per-subtype `gpu_xid_149_<hex>` checks for the Xid 149
    NETIR_LINK_EVT/NETIR_LINK_DOWN subtypes NVIDIA's decode table separates,
    each independently disableable and configurable, additive to
    `gpu_xid_149`. ([#62](https://github.com/dsx-ai-factory/health-validation/pull/62))
  - Every Xid check has an `occurrences` threshold (default 1); Xids 144-150
    have `fatal_occurrences` and `nonfatal_occurrences`.
    ([#199](https://github.com/dsx-ai-factory/health-validation/pull/199), [#205](https://github.com/dsx-ai-factory/health-validation/pull/205))
  - Every Xid code the node logs is reported. `gpu_xid_informational` is INFO
    with no action for a code NVIDIA's catalog marks Unused or IGNORE, or gives
    no resolution, and `gpu_xid_unknown` is WARNING with `monitor` for a code
    neither a check nor the catalog covers. ([#260](https://github.com/dsx-ai-factory/health-validation/pull/260))
  - Xid results list the journal lines they matched in `observed_values`, and
    Xid 13 reports the GPC, TPC, and SM. ([#241](https://github.com/dsx-ai-factory/health-validation/pull/241), [#229](https://github.com/dsx-ai-factory/health-validation/pull/229))
  - `run` and `collect` take `--xid-from` and `--xid-to` (Unix seconds), and a
    repeatable `--xid-from PCI_ADDRESS=TIMESTAMP` for one GPU, to evaluate only
    Xid events in the interval `(from, to]`. The start is exclusive and the end
    inclusive, so back-to-back windows that pass the previous `--xid-to` as the
    next `--xid-from` report an entry on the shared second once. The default is
    the whole current boot. ([#215](https://github.com/dsx-ai-factory/health-validation/pull/215), [#301](https://github.com/dsx-ai-factory/health-validation/pull/301))
- `health-validation collect` runs sensors without evaluating the catalog and
  prints the observation batch as JSON, for callers that keep evidence apart
  from evaluation. ([#81](https://github.com/dsx-ai-factory/health-validation/pull/81))
- `run` collects only the signals that runnable checks need, so a disabled
  check, a check outside `--cadence`, or one for another architecture or
  execution context causes no hardware reads. The new `Catalog.RunnableChecks`
  reports them, and `CollectAdapter` declares `Category()` and `Signals()`.
  ([#156](https://github.com/dsx-ai-factory/health-validation/pull/156))
- The toolkit bridges `collect` sensors into evaluation, with
  `toolkit/observations` (`ToMeasurements`, `FromMeasurements`) converting
  between catalog measurements and collect observations. ([#106](https://github.com/dsx-ai-factory/health-validation/pull/106),
  [#112](https://github.com/dsx-ai-factory/health-validation/pull/112))
- New collect sensors and sources. None changes a check.
  - Power and temperature, InfiniBand snapshots, and a shared kernel-message
    stream with an opt-in kernel-message patterns ([#86](https://github.com/dsx-ai-factory/health-validation/pull/86)).
  - GPU, process, storage, PCI, FUSE, pstore, and profiling families
    ([#153](https://github.com/dsx-ai-factory/health-validation/pull/153)).
  - Native PCI link speed, width, and generation ([#186](https://github.com/dsx-ai-factory/health-validation/pull/186)).
  - An embedded DCGM source for one-shot runs ([#221](https://github.com/dsx-ai-factory/health-validation/pull/221)), and DCGM
    sampling configuration with best-effort cleanup ([#278](https://github.com/dsx-ai-factory/health-validation/pull/278)).
  - A kmsg overrun keeps its `EPIPE` identity so a consumer can report lost
    records and keep sampling ([#247](https://github.com/dsx-ai-factory/health-validation/pull/247)).
- `run` logs a warning naming the GPU models when architecture discovery
  resolves to `unknown`, since every architecture-scoped check then skips.
  ([#287](https://github.com/dsx-ai-factory/health-validation/pull/287))

### Fixes

- Collect sources log a warning to stderr when they time out or fail as a
  whole, including failures a sensor reports inside its batch, as the legacy
  sensors did. Each line names the collect sensor (`sensor`) and the failing
  library or reader (`source`); the legacy lines named the toolkit sensor.
  Any failed read of a node-wide sensor, such as the CPU NUMA count, also
  warns. Errors on individual GPUs are not logged.
- `Catalog.Register`, `Get`, `List`, and `OverrideSet.ForCheck` no longer
  share slices or maps with callers. ([#175](https://github.com/dsx-ai-factory/health-validation/pull/175))
- `observed_values` reports only a check's `RequiredSignals`, not every field of
  the measurement, so a verdict no longer shows sibling signals the rules never
  read, such as `numa_node_count: 0` on `cpu_count`. A check that declares none
  keeps them all. ([#156](https://github.com/dsx-ai-factory/health-validation/pull/156))
- The Xid and RDMA link-flap reads filter journal lines in the process instead
  of passing `--grep` to `journalctl`. A line longer than the maximum fails the
  read rather than passing silently. ([#154](https://github.com/dsx-ai-factory/health-validation/pull/154))
- `nvlink_bandwidth_count_speed` names an inactive link as the reason a speed
  could not be read, instead of reporting a bare read failure.
  ([#292](https://github.com/dsx-ai-factory/health-validation/pull/292))

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
