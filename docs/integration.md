# Integrating into a GPU cluster

You run a GPU cluster and want to use health-validation to gate and observe your
nodes. This guide takes you from "I want to use this" to "it's running in my
cluster": deploy it to nodes, run it at the right point in the workload
lifecycle, act on what it reports, collect results across the fleet, tune it for
your hardware, and add a check when the catalog lacks one.

## First, the mental model

health-validation is an **on-demand, node-local readiness gate**, not a
monitoring daemon. One invocation:

- collects the current hardware/driver state of **one** node,
- evaluates it against the check catalog,
- writes a single JSON `NodeVerdict` to stdout, and
- exits.

It holds no state between runs, streams nothing, and does no cross-node
correlation. See [Architecture](/health-validation/getting-started/architecture)
for the data flow. Two consequences shape everything below:

- **You choose when it runs.** There is no built-in scheduler. You wire it into
  your workload lifecycle (job prolog, pre-flight Job, timer).
- **You own fleet aggregation.** Each run reports one node; collecting and
  correlating verdicts across the fleet is your pipeline's job (see
  [Collecting results across the fleet](#collecting-results-across-the-fleet)).

The verdict is a **point-in-time snapshot** — hardware state changes, so run it
before every job dispatch (and, for conditions that arise mid-job like Xid
events or link degradation, periodically as well).

## 1. Deploy it to nodes

Two forms ship; pick by how your nodes run workloads.

### Container image (Kubernetes, containerized runners)

Pull the published multi-arch image; the registry selects the host architecture:

```shell
docker pull ghcr.io/dsx-ai-factory/health-validation:<tag>
```

The image publishes to GitHub Container Registry (GHCR). Image tags drop the
release tag's leading `v` (release git tag `v1.2.3` → image `:1.2.3`); `latest` floats to the newest `main`. See
[Developer Guide on GitHub](https://github.com/dsx-ai-factory/health-validation/blob/main/docs/developer.md#pulling-a-published-image) for auth and how to
pull a specific tag or architecture.

For a local Linux/amd64 build that produces a loadable tarball, see
[Developer Guide on GitHub](https://github.com/dsx-ai-factory/health-validation/blob/main/docs/developer.md#build-a-linux-image-from-macos).

The image carries the CLI on an Ubuntu base — **not** the NVIDIA driver or
`journalctl`. Different check domains need different host resources mounted in;
running with the wrong flags makes the affected checks report an **error
verdict** (CRITICAL/UNKNOWN), so a misconfigured run is loud, never silently
wrong.

Use the provided wrapper, which bind-mounts the host's `journalctl` binary and
its shared-library dependencies as well as the journal resources:

```shell
scripts/health-validation-run.sh ghcr.io/dsx-ai-factory/health-validation:<tag> \
  --node-id "$NODE_NAME"
```

This requires the host and container base OS to have compatible glibc ABIs.
For a uniform fleet that is normally the case. The equivalent `docker run`
command is:

```shell
docker run --rm \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -v /run/log/journal:/run/log/journal:ro \
  -v /var/log/journal:/var/log/journal:ro \
  -v /etc/machine-id:/etc/machine-id:ro \
  -v /usr/bin/journalctl:/usr/bin/journalctl:ro \
  -v <journalctl ldd dependencies, one -v per .so, excluding libc/libpthread/libm> \
  ghcr.io/dsx-ai-factory/health-validation:<tag> \
  run --node-id "$NODE_NAME"
```

- `--gpus all -e NVIDIA_DRIVER_CAPABILITIES=utility` gives NVML the GPUs and the
  `libnvidia-ml.so.1` it needs — required for `gpu_count` and `nvlink_*`. Keep
  it `all` (a scoped subset makes `gpu_count` fire CRITICAL on visibility, not
  health).
- The journal, machine-id, `journalctl`, and its shared-library mounts are
  required for both `gpu_xid_*` and `rdma_linkflap_fatal`; without them the
  journal-backed checks report a collection failure rather than a false PASS.

The wrapper includes the complete mount set for journal-backed checks;
`gpu_xid_*` and `rdma_linkflap_fatal` need a systemd journal on the host. The
Xid read runs `journalctl --output=json --output-fields=MESSAGE`, which needs
systemd 236 or later; without a readable kernel journal for the current boot,
`gpu_xid_*` returns collection-failure verdicts.

The `rdma_topo` sensor runs `rdma_topo check` with automatic mode detection.
It requires the tool on `PATH`, its Python runtime, and read access to host PCI
sysfs. Confirmed findings are retained when collection fails; errors are
reported separately. See the [upstream help](https://github.com/linux-rdma/rdma-core/blob/master/kernel-boot/rdma_topo)
and [usage guide](https://docs.nvidia.com/multi-node-nvlink-systems/grace-blackwell-cx8-gpudirect-rdma-guide/gpudirect_rdma_testing.html#configuring-acs-using-rdma-topo-tool).

The CLI check is opt-in for both `run` and `collect`. Enable it in the YAML
file passed via `--config` (`disabled_checks` takes precedence):

```yaml
checks:
  rdma_topo: {}
```

Without this entry, `run` reports `CHECK_DISABLED` and neither command runs the
tool. The published image and wrapper do not provide `rdma_topo` or its Python
runtime; supply both in the container. Once enabled, missing dependencies
produce a CRITICAL collection-error verdict during `run`.

### Static binary (bare metal, Slurm)

Build the binary from source. The NVML backend needs cgo and NVML, so build on
a Linux GPU host:

```shell
go build -o health-validation ./toolkit/cmd/health-validation
./health-validation run
```

See [Developer Guide on GitHub](https://github.com/dsx-ai-factory/health-validation/blob/main/docs/developer.md) for the canonical Bazel build (the
production artifact is built with `bazel build`, which pins the toolchain and
link flags).

The binary links NVML via cgo, so it is a Linux artifact and needs the NVIDIA
driver and `libnvidia-ml` on the host at runtime (and `journalctl` on
PATH for Xid checks). Place it on each node (config-management, image bake, or a
shared mount) alongside any override config (below).

## 2. Wire it into the workload lifecycle

`run` is one-shot; a scheduler decides *when*. Common integrations:

### Slurm — drain on a bad verdict

Gate dispatch in a **prolog** and re-check in an **epilog**. Exit `2` means the
tool couldn't run; exit `1` means a CRITICAL/UNKNOWN verdict, and the JSON on
stdout gives the reason:

```bash
#!/bin/bash
# /etc/slurm/prolog.d/health-validation.sh
node="$SLURMD_NODENAME"
verdict=$(/opt/health/health-validation run --node-id "$node" --config /etc/health/overrides.yaml)
code=$?

if [ "$code" -eq 2 ]; then
  echo "health-validation could not run" >&2
  exit 1                                    # fail the prolog
fi
if [ "$code" -ne 0 ]; then                  # CRITICAL / UNKNOWN
  sev=$(echo "$verdict" | jq -r '.overall_severity')
  scontrol update nodename="$node" state=drain reason="health-validation:$sev"
  exit 1
fi
# Optionally ship $verdict to your fleet sink here (see §4), then:
exit 0
```

`WARNING` exits `0` by default — if you want to drain on WARNING or on
partial coverage, inspect `overall_severity` / `skipped_checks` in the JSON
rather than trusting the exit code alone.

### Kubernetes — pre-flight Job or periodic DaemonSet

There is no Helm chart or operator yet; you assemble it from the image:

- **Pre-flight per node:** a `Job` (pinned to a node via `nodeSelector`/affinity)
  that runs the image with one of the journal access arrangements below; a
  non-zero exit fails the Job, which your admission/gating flow reads.
- **Periodic:** a `DaemonSet` (or per-node `CronJob`) that runs on a timer and
  writes the verdict to stdout, where your cluster logging picks it up. Reflect
  a bad verdict onto the node with a taint or a `NodeCondition` via your own
  controller/`node-problem-detector` custom plugin — health-validation reports;
  acting on the report is your controller's job.

The NVIDIA device-plugin allocation must expose all node GPUs to the pod for
`gpu_count` and `nvlink_*`. Journal-backed checks (`gpu_xid_*` and
`rdma_linkflap_fatal`) need one of these arrangements:

1. **Explicit read-only hostPaths (least host exposure).** Mount the host's
   journal directories, `/etc/machine-id`, `journalctl`, and every non-glibc
   dependency reported by `ldd $(command -v journalctl)` at the same paths in
   the container. This is the Kubernetes equivalent of
   `scripts/health-validation-run.sh`; it requires no change to the image. The
   dependency paths are host-specific, so this profile is described rather
   than represented by a portable manifest below.
2. **Read-only host root plus a `journalctl` chroot shim (simpler operations).**
   Mount the host root at `/host` read-only. Put an executable named
   `journalctl` ahead of `/usr/bin` in the container's `PATH` that contains:

   ```sh
   #!/bin/sh
   exec /usr/sbin/chroot /host /usr/bin/journalctl "$@"
   ```

   The health-validation binary continues to run in the container; only its
   `journalctl` child processes use the host filesystem. The shim must be
   supplied by a small image overlay or another executable volume: the stock
   image deliberately does not include `journalctl`. Run this pod as UID 0
   with `SYS_CHROOT`; it does not need `hostPID`, `nsenter`, or `privileged`.

Both options use `hostPath`, which Kubernetes Pod Security Baseline and
Restricted policies prohibit. Keep every hostPath read-only, place this
workload only in an operator-approved namespace, and grant no broader host
access than needed. The chroot option exposes the whole host filesystem
read-only, so prefer explicit mounts where that policy boundary matters.

#### Security context and check coverage

An operator-approved namespace here means one allowed to use `hostPath`; it
does **not** mean the pod needs `privileged: true`. The intended profiles are:

| Profile | Host access and security context | Checks with complete coverage |
| --- | --- | --- |
| Docker wrapper | Read-only journal, machine-id, `journalctl`, and its `ldd` dependencies; `--gpus all` and `NVIDIA_DRIVER_CAPABILITIES=utility`; no `--privileged`, host PID namespace, or extra capabilities. | All default CLI checks, provided the container sees all node GPUs. |
| Kubernetes explicit mounts | The same read-only paths as the wrapper. `runAsUser: 0`, `allowPrivilegeEscalation: false`, and `capabilities.drop: ["ALL"]`; no added capabilities, `privileged`, host PID namespace, or host networking. | All default CLI checks, provided the pod requests all node GPUs through the NVIDIA device plugin. |
| Kubernetes host-root chroot | Read-only `/` hostPath at `/host`, plus the shim above. The same settings as explicit mounts, except add `SYS_CHROOT`. No `privileged`, host PID namespace, `SYS_ADMIN`, or `nsenter`. | All default CLI checks, provided the pod requests all node GPUs through the NVIDIA device plugin. |
| GPU access but no usable host journal | GPU allocation only, or a journal mount/chroot that `journalctl` cannot read. | GPU/NVLink and CPU/NUMA checks can run; `gpu_xid_*` and `rdma_linkflap_fatal` return collection-failure verdicts rather than PASS. |

The image runs as root by default. An explicitly non-root Docker user or pod
may be viable only when its host-mapped UID/GID has journal-read permission;
that setup is deployment-specific and should be validated before relying on
journal-backed checks. CPU/NUMA checks need no elevated privilege, but observe
the sysfs view supplied by the container runtime.

The following is a complete chroot-profile example. Replace the image, target
node, and GPU count with values for your fleet. The GPU limit must equal the
node's full GPU count: a smaller allocation intentionally makes `gpu_count`
report CRITICAL.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: health-validation-journalctl-shim
data:
  journalctl: |
    #!/bin/sh
    exec /usr/sbin/chroot /host /usr/bin/journalctl "$@"
---
apiVersion: batch/v1
kind: Job
metadata:
  name: health-validation-preflight
spec:
  backoffLimit: 0
  template:
    spec:
      nodeName: <target-node-name>
      restartPolicy: Never
      containers:
        - name: health-validation
          image: <registry>/health-validation:<tag>
          args: ["run", "--node-id=$(NODE_NAME)"]
          env:
            - name: NODE_NAME
              valueFrom:
                fieldRef:
                  fieldPath: spec.nodeName
            - name: PATH
              value: /opt/host-journalctl:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
            - name: NVIDIA_DRIVER_CAPABILITIES
              value: utility
          resources:
            limits:
              nvidia.com/gpu: <all-gpus-on-node>
          securityContext:
            runAsUser: 0
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
              add: ["SYS_CHROOT"]
          volumeMounts:
            - name: host-root
              mountPath: /host
              readOnly: true
            - name: journalctl-shim
              mountPath: /opt/host-journalctl
              readOnly: true
      volumes:
        - name: host-root
          hostPath:
            path: /
            type: Directory
        - name: journalctl-shim
          configMap:
            name: health-validation-journalctl-shim
            defaultMode: 0555
```

For periodic operation, use the same container, security context, environment,
and volumes in a `DaemonSet`, replace `nodeName` with a fleet `nodeSelector` or
affinity rule, and run the one-shot command in a loop:

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: health-validation
spec:
  selector:
    matchLabels:
      app: health-validation
  template:
    metadata:
      labels:
        app: health-validation
    spec:
      containers:
        - name: health-validation
          image: <registry>/health-validation:<tag>
          command: ["/bin/sh", "-c"]
          args:
            - |
              while true; do
                /health-validation run --node-id "$NODE_NAME"
                sleep 15m
              done
          # Copy env, resources, securityContext, and volumeMounts from the Job.
      # Copy volumes from the Job, then add nodeSelector/affinity and tolerations
      # appropriate for the nodes being validated.
```

### Standalone / timer

A `cron` entry or `systemd` timer running `health-validation run` and piping
stdout to your log shipper is a complete integration for non-scheduler fleets.

> **Cadence note.** Omit `--cadence` and all enabled
> checks run on every invocation. Pass `--cadence=CONTINUOUS|PROLOG|MAINTENANCE`
> to run checks whose declared cadence covers that window (Continuous
> ⊂ Prolog ⊂ Maintenance); checks outside it are reported as
> `CADENCE_MISMATCH` skips. A job-time run that must not evaluate Prolog checks
> uses `--cadence=CONTINUOUS`.

> **Execution context.** `run` defaults to `--execution-context=TENANT_GUEST`,
> which suits a tenant VM, container, or Kubernetes worker. On the host OS or a
> privileged control-plane VM, pass `--execution-context=OPERATOR_INFRA` so
> checks that need full hardware access are not skipped
> (`SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL`).

## 3. Read and act on the verdict

Two signals, coarse then precise.

**Exit code** (for shell gating): `0` = PASS/INFO/WARNING, `1` =
CRITICAL/UNKNOWN, `2` = the tool could not produce a verdict (config/backend/
collection/output failure — stdout is empty, error on stderr). It does **not**
distinguish CRITICAL from UNKNOWN or INFO from WARNING.

**The JSON `NodeVerdict`** (for anything precise) — a versioned contract you can
validate against the published
[JSON Schema](https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/node-verdict.schema.json):

- **`catalog_version`** — the catalog module compiled into the producer. Compare
  this with the version your integration expects when you need to confirm that
  a check-logic or default-threshold fix was present. Custom checks, overrides,
  and disabled checks do not change this value.
- **`overall_severity`** — the authoritative level (`PASS`/`INFO`/`WARNING`/
  `CRITICAL`/`UNKNOWN`). Gate on this, not only the exit code.
- **`recommended_action`** (on each check result; `NodeVerdict` carries no
  node-level `recommended_action`) — what to do. It is present **only when
  there is an action** and **omitted entirely** otherwise, which is the common
  healthy case. Treat its absence as "no action" and guard for it before
  reading into it. When present it has the form
  `{operational_state, remediation_actions}`, for example
  `{"operational_state":"drain","remediation_actions":["reset_gpu","restart_os","service_required"]}`.
  - `operational_state` is `monitor` or `drain`. It is never `ready`; that case
    is the omitted field.
  - `remediation_actions` is an **ordered sequence** of remediation steps,
    present only when `operational_state` is `drain`. Feed it to your
    remediation engine in order. Its entries are an open string set, not a
    closed enum: this repo's checks use `reset_gpu`, `restart_os`,
    `service_required`, and others, and a custom catalog may add its own. Treat
    an unrecognized value as an opaque step, not an error.
  - Each check carries its own remediation sequence, and nothing ranks them at
    the node level, so walk `check_results` for the full remediation picture.
    See [Remediation Actions](/health-validation/operating/remediation) for the
    action catalog and how to work through several failing checks. In short,
    re-run the whole suite after each step; a `service_required` step, then
    severity, decides which failing check to act on next.
- **`affected_components`** / **`severity_by_domain`** — which domain and which
  `check_ids`/entities failed, so you route (GPU vs NVLink vs Xid) without
  re-parsing every result.
- **`check_results`** — per-check `severity`, `messages`, `observed_values`, and
  the resolved `thresholds` (with `overridden`), for the human-readable why.
- **`condition_scope`** (on each check result) — how far the detected condition
  reaches: `NODE`, `DOMAIN` (the NVLink domain shared with peer nodes), or
  `CLUSTER` (the network fabric beyond the node). Route escalation on this
  instead of inferring it from the check ID. A result whose measurement could
  not be collected reports `NODE`, so it never sends you off-node on no evidence.
  Entries in `skipped_checks` carry the declared value, so gate on `skipped`
  first.
- **`skipped_checks`** — checks that did **not** run, with a `skip_reason`
  (`THRESHOLD_UNCONFIGURED`, `CHECK_DISABLED`, `SENSOR_MISSING`, …).
  `SENSOR_MISSING` is the one to alert on: a whole domain reported nothing, so
  those checks were never assessed — distinct from `CHECK_DISABLED` or
  `SIGNAL_UNAVAILABLE_ARCHITECTURE`, which are deliberate. Skip *count* is not
  a coverage signal, since most skips are by design; if coverage matters to
  your gate, filter `skipped_checks` on `skip_reason`.

Set `--node-id` to your scheduler/inventory identifier (it defaults to the
hostname) so the verdict correlates with the rest of your fleet data.

## 4. Collecting results across the fleet

The tool is deliberately node-local — it does **not** aggregate. Fleet
visibility is a pipeline you build around it, and the design makes that easy:
every run emits one schema-stable JSON object carrying `catalog_version`,
`node_id`, `timestamp`, and `overall_severity`.

A workable pattern:

1. **Capture** stdout at the point of invocation — the Slurm prolog, the K8s
   Job/DaemonSet (container stdout), or the timer.
2. **Ship** it to a central sink: your cluster logging stack (Elasticsearch/
   Loki/Splunk), an object store keyed by `node_id`+`timestamp`, or a database.
   The JSON validates against the published schema, so ingestion and indexing
   are mechanical.
3. **Aggregate/query** there: index on `node_id`, `overall_severity`,
   `affected_components[].category`, and `check_results[].check_id` to answer
   "which nodes are draining and why," "which checks fire most across the
   fleet," and "how `skipped_checks` reasons trend."

Because a verdict is a point-in-time snapshot, store each run rather than
overwriting — the history is what turns per-node gating into fleet insight.

If you want to keep the raw node-local measurements and evaluate them
elsewhere, run `health-validation collect` instead of `run`. It emits the
sensor readings as an `ObservationBatch` (protojson) with no verdict, so ship
and store it the same way as a verdict.

### Pushing metrics to a collector

Shipping the JSON is the complete record; publishing metrics is the cheap
summary that feeds dashboards and alerts. Enable it in the config file:

```yaml
metrics:
  enabled: true
  endpoint: http://127.0.0.1:44318
```

The full metric set, the resource attributes, and the severity-ordinal encoding
are documented in the [README](https://github.com/dsx-ai-factory/health-validation/blob/main/README.md#publishing-metrics). Two properties
matter for integration: publishing never changes the verdict or the exit code —
a dead collector is a logged warning, not a failed health check — and the JSON
verdict is written to stdout before the push, so your capture step in the
pipeline above is unaffected by collector latency.

#### Publishing metrics from a container

The wrapper and the `docker run` invocation above deliberately do **not** use
host networking, and the Kubernetes guidance below explicitly rules it out. So
`http://127.0.0.1:44318` inside the container resolves to the *container's* own
loopback, not the node's — the collector never receives anything, and the only
symptom is a warning line on stderr.

Pick one, in order of preference:

1. **Point at a routable address.** If the collector listens on a node IP or a
   cluster service, use that; nothing about the container changes. In
   Kubernetes this is usually a collector DaemonSet reached through
   `status.hostIP` or a service DNS name.
2. **Publish to the host gateway.** Add
   `--add-host=host.docker.internal:host-gateway` to the run invocation and set
   `endpoint: http://host.docker.internal:44318`. The collector must be
   listening on an address reachable from the bridge network, not only on
   `127.0.0.1`.
3. **Host networking** (`--network host`, or `hostNetwork: true`). This works
   with a loopback-only collector and needs no collector change, but it gives up
   the network isolation the security posture below relies on. Weigh it
   accordingly; it is the reason the stock wrapper does not do it for you.

## 5. Tune thresholds for your fleet (overrides)

Most checks carry thresholds you can override per node-class with a YAML config,
passed via `--config` (or mounted into the container). Each `checks.<id>.<param>`
becomes a threshold override, coerced to the threshold's declared type:

```yaml
# /etc/health/overrides.yaml
checks:
  gpu_count:
    expected_gpu_count: 8          # your node class has 8 GPUs
```

Governance rules:

- **Default thresholds** are overridable (e.g. `expected_gpu_count`).
- **Required-override** thresholds have no usable default: until you supply one,
  the check **skips** with `skip_reason: THRESHOLD_UNCONFIGURED`. Configure these
  per node-class.
- **Fixed thresholds** can never be overridden; an attempt is rejected.

Maintain one config per node-class (GB200 vs H100, expected GPU counts) and
select it at invocation. To turn a check off entirely, list it under `disabled_checks:` in the config
(or use `catalog.WithDisabledChecks` when embedding the library). It then
appears in `skipped_checks` with `skip_reason: CHECK_DISABLED`.

## 6. Add a check the catalog doesn't have

A check is **data**, not code: a `CheckDefinition` with required signals, a CEL
rule, threshold governance, and a recommended action, evaluated by a generic
engine. Two paths:

- **Contribute it upstream** (it belongs in the shared catalog): add signals to
  the domain measurement, write the `CheckDefinition` factory, and register it in
  `builtinChecks()`. The full flow — classification, CEL pattern, tests, and the
  build gates — is in [`AGENTS.md`](https://github.com/dsx-ai-factory/health-validation/blob/main/AGENTS.md) ("Adding a check" / "Adding a
  sensor for a new domain"). Contributions are not yet open externally; see
  [`CONTRIBUTING.md`](https://github.com/dsx-ai-factory/health-validation/blob/main/CONTRIBUTING.md).
- **Keep it private** (proprietary thresholds or signals you won't upstream):
  import the `catalog`/`health` packages, author your `CheckDefinition`s with
  `Provenance: health.Private`, and register them alongside the built-ins without
  forking. The catalog routes a measurement to your check when they share a
  `Category` and the measurement carries the check's `RequiredSignals`; validate
  that coupling in tests with `catalog.CheckSignalUniqueness` and
  `Catalog.CheckCoverage`. [Extending the Catalog](/health-validation/operating/extending) is the full
  walkthrough — measurement, signals, the private `CheckDefinition`, wiring a
  backend, and tests.

Either way, the durable surface is the **catalog** (check definitions and the
evaluation engine); the toolkit's sensor/backend layer that feeds it is a
reference implementation and may change.

## Shared raw collection library

Use the experimental `github.com/dsx-ai-factory/health-validation/collect`
module when you need raw observations rather than a catalog verdict.
`collect.Sensor` keeps the same `ID`, `Signals`, and `Measure(ctx, ...OpOption)`
interface. The packages below collect and normalize source data; they do not
apply thresholds, infer health, remediate, write an event database, or schedule
future collection. `agentkit/server` can serve registered sensors without
coupling them to the toolkit's check catalog.

### Families and constructors

Package paths in this table are relative to `…/health-validation/collect/`.
Several families share the NVML sensor and its caller-owned instance.

| Family | Raw data | Constructor |
| --- | --- | --- |
| GPU inventory and telemetry | Structured GPU inventory; temperature, power, framebuffer memory, utilization, clocks, and device metadata | `sensors/nvml.New(inst)`; request inventory with `collect.WithGPUInventory()` |
| GPU memory reliability | ECC modes and counts by location and lifetime; row-remapper counts and native status | `sensors/nvml.New(inst)` |
| NVLink and fabric | Local endpoint state and error counters; fabric state, status, identity, and health fields | `sensors/nvml.New(inst)` |
| GPU processes | Compute-process membership, process count, framebuffer use, and host process name/state | `sensors/nvml.New(inst)` or explicit diagnostic options with `NewWithOptions` |
| Linux processes | Current process state, name, boot/start identity, and visible process totals/state counts | `sensors/process.New(fsys)` |
| Host memory, CPU, and resources | Memory and BPF JIT bytes; CPU topology, cumulative mode times and load; kernel file handles, visible descriptors, and task counters | `sensors/memory.New(fsys)`, `sensors/cpu.New(fsys)`, `sensors/host.New(fsys)` |
| Block devices and filesystems | Block identity, metadata, size and parents; mount metadata and eligible filesystem capacity | `sensors/disk.New(fsys, ...source/disk.Option)` |
| FUSE | Connection waiting/background/congestion counts and mount correlation | `sensors/fuse.New(fsys, ...source/fuse.Option)` |
| PCI | PCI address, vendor/device/class identity, driver, bound module, and raw ACS control | `sensors/pci.New(fsys)` |
| `rdma_topo` | ACS and IOMMU group findings and invocation evidence | `sensors/rdmatopo.New(nil)` |
| Persistent crash records | Live and archived pstore paths, text or base64 content, hashes, and root provenance | `sensors/pstore.New(fsys)` or `NewWithRoots(fsys, roots...)` |

GPM profiling is a separate, opt-in NVML sensor:
`sensors/nvml.NewGPM(inst, sampleDuration, metricIDs...)`. The duration must be
**greater than 100 milliseconds**. It takes two native samples and retains
`sample_started_at` and `sample_ended_at`; its percentage signals are not DCGM
profiling ratios. Do not add it to routine collection unless the sampling cost
and interval are appropriate for your application. The existing DCGM,
InfiniBand, and kernel-message sensors remain separate source adapters.
GPM holds the process-wide NVML gate across both samples, the configured wait,
and sample cleanup to prevent shutdown from invalidating live samples. Serialize
it with ordinary NVML collection, `Refresh`, and `Shutdown`, including calls on
another instance in the same process. Overlapping source calls return `ErrInUse`,
which sensors report as unavailable observations. A collection timeout does not
release the gate before native work and frees finish.
Clock-event queries use the legacy throttle-reason API before driver 535.
`gpu.memory.frame_buffer.used_ratio` matches DCGM's `Used / (Total - Reserved)`
definition; legacy NVML and unified-memory fallbacks that do not report reserved
memory emit unsupported rather than `Used / Total`.
Chassis serial is reported only from platform-info version 2. The pinned NVML
binding requests version 1, so that field currently reports unsupported rather
than presenting rack GUID bytes as a chassis serial.

### Register and collect

Register only the sources your service needs, before it handles requests:

```go
import (
    "github.com/dsx-ai-factory/health-validation/agentkit/server"
    "github.com/dsx-ai-factory/health-validation/collect/sensors/cpu"
    "github.com/dsx-ai-factory/health-validation/collect/sensors/host"
    "github.com/dsx-ai-factory/health-validation/collect/sensors/memory"
)

func rawServer() *server.Server {
    service := server.New()
    service.Register(cpu.New(nil), memory.New(nil), host.New(nil))
    return service
}
```

For direct calls, `collect.WithSignalID(ids...)` selects canonical definitions.
An unknown ID is an error; `collect.WithSignalID()` selects nothing and does no
source I/O. `collect.WithCycle(cycle)` supplies collection identity, start time,
and the collection timeout. Direct calls on file-backed sensors do not require
an explicit cycle. Native calls and
blocking acquisitions retain their own admission gates: a timeout stops the
wait, not an active kernel or cgo call. Reuse the source and sensor; do not
create replacements to bypass a gate. Close a caller-owned NVML instance only
after its outstanding calls finish.
The agentkit server and one-shot collection example request GPU inventory from
the ordinary NVML and DCGM sensors and retain both source-labelled snapshots
in the returned batch. Other sensors do not receive the inventory option.

### Identity, values, and outcomes

Canonical definitions live in `collect/signals`; source adapters and sensors
reuse them. Observations use typed targets rather than names encoded into
opaque JSON values:

- `ProcessRef`: boot ID, PID, and process start ticks; PID alone is not identity.
- `GpuProcessRef`: a `GpuRef` and a `ProcessRef`.
- `BlockDeviceRef`: kernel major and minor numbers.
- `MountRef`: boot-scoped mount namespace and kernel mount ID, not the mount path alone.
- `PCIRef`: PCI address, including domain, bus, device, and function.
- `FuseConnectionRef`: boot ID and connection ID.
- `PstoreRecordRef`: record path and SHA-256 of the bytes read.

These extend the existing node, GPU, NVLink endpoint, ordered GPU-pair,
InfiniBand, and kernel-record targets. Inventory remains structured in
`ObservationBatch.inventory`; inspect each GPU entry's `field_errors` before
treating an absent attribute as unattempted or unsupported.

Unsigned counts and byte values use `Value.uint_value`, exposed in Go as
`observation.Value_UintValue`. Protobuf JSON encodes `uint64` values as decimal
**strings**, for example `{"uintValue":"18446744073709551615"}`. Preserve the
string or parse it with an unsigned 64-bit integer type. A JSON number or
JavaScript `Number` can lose precision. Units remain part of each canonical
definition; do not assume every memory or clock signal uses the same unit.

Inspect the outcome before reading the value. `CollectionError` distinguishes
permission, parse, timeout, unsupported, and source failures. `NoValue` records
successful collection with no target or no supported value. Neither outcome
is a healthy zero. In particular, denied `/proc/vmallocinfo` reads are failures,
and NVML unavailable-memory sentinels are not byte measurements.

### Process and filesystem exposure

Process names use `/proc/<pid>/comm` by default. Full command lines are an
explicit GPU-process diagnostic option:
`nvml.WithProcessCmdlineDiagnostics(true)` with `nvml.NewWithOptions`.
Command lines can contain credentials or other sensitive arguments; restrict
access and retention before enabling them. Reads are bounded, report
truncation, and can still block in the kernel. No sensor reads process
`environ`. Process collection reports current membership and state, not
historical per-process utilization. CPU activity reports cumulative mode
seconds, not percentages synthesized from earlier runs. `/proc/stat` requires
the original user, nice, system, and idle fields; a truncated aggregate row
reports a parse error. Later mode fields are kernel-dependent: an absent
selected field reports unsupported, while a present malformed field reports
a parse error independently of other selected fields.
Non-UTF-8 process names or command-line bytes report a parse error instead of
making the batch unencodable. Diagnostic truncation preserves complete UTF-8
characters within the byte limit.

File-backed constructors accept an `fs.FS`; `nil` means `os.DirFS("/")`.
An injected filesystem must expose paths relative to that root, such as
`proc/meminfo` or `sys/class/block`. A container's root, PID view, and mount
namespace are not automatically the host's. Align those views explicitly;
otherwise process counts and mount identity describe the visible namespace.
Kernel file handles count file descriptions; descriptor totals count visible
per-process descriptor entries, which can reference the same description.

Disk capacity is separate from filesystem metadata. An injected `fs.FS` does
not implicitly call live-host `statfs`. Supply `source/disk.WithHostRoot(root)`
or an explicit `WithStatfs` callback for capacity. When the native capacity root
is not `/`, you must also supply `source/disk.WithMountPID(pid)`. This option
selects `proc/<pid>/mountinfo` and `proc/<pid>/ns/mnt` in the supplied filesystem.
Verify that the selected process uses the same mount view as the capacity root.
PID 1 is not automatically the host process; the collector makes no such assumption.
Without an explicit PID, metadata uses `proc/self`, and alternate-root native
capacity returns an unsupported acquisition error rather than mixing views.
The `WithStatfs` caller owns identity alignment with the injected mount records;
the collector does not compare synthetic records with live-host mount IDs.

The default mount namespace combines the boot ID with the selected process's
mount-namespace inode. Use `WithNamespace` for a view that cannot provide those
identities, with equivalent boot/reuse scope. FUSE accepts its own
`source/fuse.WithMountPID(pid)` and `source/fuse.WithNamespace` options for
correlation. Keep mount metadata, namespace identity, and capacity in one view.

On Linux, native capacity checks the opened descriptor's `STATX_MNT_ID` against
the enumerated mount ID before calling `Fstatfs` on that same descriptor. A
replacement or mismatched mount view returns an unavailable acquisition error,
without attaching capacity to the old target. If the kernel or platform cannot
provide this identity, native capacity returns an unsupported error. Injected
capacity callbacks remain usable on other platforms.

Capacity acquisition permits only known local filesystem types. It skips
autofs, FUSE, network filesystems, unknown types, and stacked mounts with an
ambiguous path; their metadata can still be collected. Native capacity paths
are confined to the configured root. The statfs gate remains held until a
timed-out syscall finishes. FUSE collection reads the control filesystem and
mount metadata; it does not probe files inside a FUSE mount or write `abort`.
Ramfs has no capacity accounting and is skipped. A native filesystem reporting
zero capacity blocks returns an unsupported outcome, not a zero-byte capacity.
Disk text and archived pstore paths that are not valid UTF-8 report parse errors.

PCI addresses retain domain-qualified identity, including four- to eight-digit
domains used by VMD devices. ACS traversal preserves raw registers and follows
placeholder capabilities without interpreting them as health policy.

FUSE correlation uses the kernel `dev_t` identity, `(major << 20) | minor`,
so both anonymous-device FUSE mounts and block-device `fuseblk` mounts match
their control connections. Bind mounts remain separate correlations. A connection
with no mounts in the selected view returns an empty string for each correlation
list. `NO_TARGETS` is source-scoped and means no visible control connections.

Pstore scans `sys/fs/pstore` and `var/lib/systemd/pstore` by default, with
bounded record reads and no deletion. `NewWithRoots` accepts other relative
record directories. Its observation timestamp is the **scan time**, not the
crash time; a persistent record can come from an earlier boot. Use its content
and path/hash identity without inventing a crash timestamp.

---

For the JSON contract, see
[`catalog/health/node-verdict.schema.json`](https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/node-verdict.schema.json);
for build/publish mechanics, [Developer Guide on GitHub](https://github.com/dsx-ai-factory/health-validation/blob/main/docs/developer.md); for the design,
[Architecture](/health-validation/getting-started/architecture).
