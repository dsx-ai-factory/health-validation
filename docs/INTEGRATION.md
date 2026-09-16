# Integrating into a GPU cluster

You run a GPU cluster and want health-validation gating and observing your
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
correlation — [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) covers the data flow.
Two consequences shape everything below:

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
release tag's `toolkit/` prefix and leading `v` (release git tag
`toolkit/v1.2.3` → image `:1.2.3`); `latest` floats to the newest `main`. See
[`docs/DEVELOPER.md`](DEVELOPER.md#pulling-a-published-image) for auth and how to
pull a specific tag or architecture.

For a local Linux/amd64 build that produces a loadable tarball, see
[`docs/DEVELOPER.md`](DEVELOPER.md#build-a-linux-image-from-macos).

The image carries the CLI on an Ubuntu base — **not** the NVIDIA driver or
`journalctl`. Different check domains need different host resources mounted in;
running with the wrong flags makes the affected checks report an **error
verdict** (CRITICAL/UNKNOWN), so a misconfigured run is loud, never silently
wrong:

Use the provided wrapper, which bind-mounts the host's `journalctl` binary and
its shared-library dependencies as well as the journal resources:

```shell
scripts/health-validation-run.sh ghcr.io/dsx-ai-factory/health-validation:<tag> \
  --node-id "$NODE_NAME"
```

This requires the host and container base OS to have compatible glibc ABIs.
For a uniform fleet that is normally the case. Its equivalent `docker run`
invocation is:

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
`gpu_xid_*` and `rdma_linkflap_fatal` need a systemd journal on the host.


### Static binary (bare metal, Slurm)

Build the binary from source (it needs cgo + NVML on a Linux GPU host for the
NVML backend):

```shell
go build -o health-validation ./toolkit/cmd/health-validation
./health-validation run
```

See [`docs/DEVELOPER.md`](DEVELOPER.md) for the canonical Bazel build (the
production artifact is built with `bazel build`, which pins the toolchain and
link flags).

The binary links NVML via cgo, so it is a Linux artifact and needs the NVIDIA
driver + `libnvidia-ml` present on the host at runtime (and `journalctl` on
PATH for Xid checks). Place it on each node (config-management, image bake, or a
shared mount) alongside any override config (below).

## 2. Wire it into the workload lifecycle

`run` is one-shot; a scheduler decides *when*. Common integrations:

### Slurm — drain on a bad verdict

Gate dispatch in a **prolog** and re-check in an **epilog**. Exit `2` means the
tool couldn't run; exit `1` means a CRITICAL/UNKNOWN verdict; the JSON is on
stdout for the reason:

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
| Docker wrapper | Read-only journal, machine-id, `journalctl`, and its `ldd` dependencies; `--gpus all` and `NVIDIA_DRIVER_CAPABILITIES=utility`; no `--privileged`, host PID namespace, or extra capabilities. | All stock CLI checks, provided the container sees all node GPUs. |
| Kubernetes explicit mounts | The same read-only paths as the wrapper. `runAsUser: 0`, `allowPrivilegeEscalation: false`, and `capabilities.drop: ["ALL"]`; no added capabilities, `privileged`, host PID namespace, or host networking. | All stock CLI checks, provided the pod requests all node GPUs through the NVIDIA device plugin. |
| Kubernetes host-root chroot | Read-only `/` hostPath at `/host`, plus the shim above. The same settings as explicit mounts, except add `SYS_CHROOT`. No `privileged`, host PID namespace, `SYS_ADMIN`, or `nsenter`. | All stock CLI checks, provided the pod requests all node GPUs through the NVIDIA device plugin. |
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

> **Cadence note.** Omit `--cadence` and all registered
> checks run on every invocation. Pass `--cadence=CONTINUOUS|PROLOG|MAINTENANCE`
> to run checks whose declared cadence covers that window (Continuous
> ⊂ Prolog ⊂ Maintenance); checks outside it are reported as
> `CADENCE_MISMATCH` skips. A job-time run that must not evaluate Prolog checks
> uses `--cadence=CONTINUOUS`.

## 3. Read and act on the verdict

Two signals, coarse then precise.

**Exit code** (for shell gating): `0` = PASS/INFO/WARNING, `1` =
CRITICAL/UNKNOWN, `2` = the tool could not produce a verdict (config/backend/
collection/output failure — stdout is empty, error on stderr). It does **not**
distinguish CRITICAL from UNKNOWN or INFO from WARNING.

**The JSON `NodeVerdict`** (for anything precise) — a versioned contract you can
validate against the published
[JSON Schema](../catalog/health/node-verdict.schema.json):

- **`catalog_version`** — the catalog module compiled into the producer. Compare
  this with the version your integration expects when you need to confirm that
  a check-logic or default-threshold fix was present. Custom checks, overrides,
  and disabled checks do not change this value.
- **`overall_severity`** — the authoritative level (`PASS`/`INFO`/`WARNING`/
  `CRITICAL`/`UNKNOWN`). Gate on this, not only the exit code.
- **`recommended_action`** (on each check result — `NodeVerdict` carries no
  node-level `recommended_action`) — what to do, present **only when there is
  an action**. The field is **omitted entirely** when none is needed (the
  common healthy-check case), so treat its absence as "no action" and guard
  for it before reading into it — don't assume it's there. When present it is
  `{operational_state, remediation_actions}` (e.g.
  `{"operational_state":"drain","remediation_actions":["reset_gpu","restart_os","service_required"]}`):
  `operational_state` is `monitor` or `drain` (never `ready` — that case is the
  omitted field), and `remediation_actions` is an **ordered sequence** of
  remediation steps, present only when `operational_state` is `drain` — feed
  it to your remediation engine in order. `remediation_actions` entries are an
  open string set, not a closed enum (`reset_gpu`, `restart_os`,
  `service_required`, … from this repo's own checks, but a custom catalog may
  add its own); treat an unrecognized value as an opaque step, not an error. Since
  each check carries its own remediation sequence and there is no single
  node-level ranking across them, walk `check_results` for a full remediation
  picture. See [`docs/REMEDIATION.md`](REMEDIATION.md) for the full action
  catalog and how to prioritize across multiple failing checks and work
  through a sequence (re-run the whole suite after each step; a
  `service_required` step, then severity, decides which failing check to act
  on next).
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

### Pushing metrics to a collector

Shipping the JSON is the complete record; publishing metrics is the cheap
summary that feeds dashboards and alerts. Enable it in the config file:

```yaml
metrics:
  enabled: true
  endpoint: http://127.0.0.1:44318
```

The full metric set, the resource attributes, and the severity-ordinal encoding
are documented in the [README](../README.md#publishing-metrics). Two properties
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
select it at invocation. To turn a check off entirely, disable it
(programmatically via `catalog.WithDisabledChecks`, when embedding the library) —
it then appears in `skipped_checks` with `skip_reason: CHECK_DISABLED`.

## 6. Add a check the catalog doesn't have

A check is **data**, not code: a `CheckDefinition` with required signals, a CEL
rule, threshold governance, and a recommended action, evaluated by a generic
engine. Two paths:

- **Contribute it upstream** (it belongs in the shared catalog): add signals to
  the domain measurement, write the `CheckDefinition` factory, and register it in
  `builtinChecks()`. The full flow — classification, CEL pattern, tests, and the
  build gates — is in [`AGENTS.md`](../AGENTS.md) ("Adding a check" / "Adding a
  sensor for a new domain"). Contributions are not yet open externally; see
  [`CONTRIBUTING.md`](../CONTRIBUTING.md).
- **Keep it private** (proprietary thresholds or signals you won't upstream):
  import the `catalog`/`health` packages, author your `CheckDefinition`s with
  `Provenance: health.Private`, and register them alongside the built-ins without
  forking. The catalog routes a measurement to your check when they share a
  `Category` and the measurement carries the check's `RequiredSignals`; validate
  that coupling in tests with `catalog.CheckSignalUniqueness` and
  `Catalog.CheckCoverage`. [`docs/EXTENDING.md`](EXTENDING.md) is the full
  walkthrough — measurement, signals, the private `CheckDefinition`, wiring a
  backend, and tests.

Either way, the durable surface is the **catalog** (check definitions and the
evaluation engine); the toolkit's sensor/backend layer that feeds it is a
reference implementation and may change.

---

For the JSON contract, see
[`catalog/health/node-verdict.schema.json`](../catalog/health/node-verdict.schema.json);
for build/publish mechanics, [`docs/DEVELOPER.md`](DEVELOPER.md); for the design,
[`docs/ARCHITECTURE.md`](ARCHITECTURE.md).
