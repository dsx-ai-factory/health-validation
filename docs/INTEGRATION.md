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

Pull the multi-arch image (host arch is selected automatically):

```shell
docker pull nvcr.io/<org>/<team>/health-validation:<tag>
```

The image publishes to NVIDIA's NGC registry (NVCR, `nvcr.io`). Image tags drop
the release tag's `toolkit/` prefix and leading `v` (release git tag
`toolkit/v1.2.3` → image `:1.2.3`); `latest` floats to the newest `main`. See
[`docs/DEVELOPER.md`](DEVELOPER.md#pulling-a-published-image) for auth and how to
pull a specific tag or architecture.

The image carries the CLI and `journalctl` on an Ubuntu base — **not** the
NVIDIA driver. Different check domains need different host resources mounted in;
running with the wrong flags makes the affected checks report an **error
verdict** (CRITICAL/UNKNOWN), so a misconfigured run is loud, never silently
wrong:

```shell
docker run --rm \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -v /run/log/journal:/run/log/journal:ro \
  -v /var/log/journal:/var/log/journal:ro \
  -v /etc/machine-id:/etc/machine-id:ro \
  nvcr.io/<org>/<team>/health-validation:<tag> \
  run --node-id "$NODE_NAME"
```

- `--gpus all -e NVIDIA_DRIVER_CAPABILITIES=utility` gives NVML the GPUs and the
  `libnvidia-ml.so.1` it needs — required for `gpu_count` and `nvlink_*`. Keep
  it `all` (a scoped subset makes `gpu_count` fire CRITICAL on visibility, not
  health).
- The journal + machine-id mounts are required for `gpu_xid_*`; without them
  those checks report **UNKNOWN** rather than a false PASS.

[`docs/DEVELOPER.md`](DEVELOPER.md#running-on-a-gpu-node) has the exhaustive
per-domain mount table and the container limitations (notably: `gpu_xid_*`
needs a systemd journal).

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
  that runs the image with the mounts above; a non-zero exit fails the Job, which
  your admission/gating flow reads.
- **Periodic:** a `DaemonSet` (or per-node `CronJob`) that runs on a timer and
  writes the verdict to stdout, where your cluster logging picks it up. Reflect
  a bad verdict onto the node with a taint or a `NodeCondition` via your own
  controller/`node-problem-detector` custom plugin — health-validation reports;
  acting on the report is your controller's job.

### Standalone / timer

A `cron` entry or `systemd` timer running `health-validation run` and piping
stdout to your log shipper is a complete integration for non-scheduler fleets.

> **Cadence note.** All registered checks run on **every** invocation — the CLI
> does not (yet) filter by a check's declared `SchedulingCadence`. So you get the
> full suite each run; control frequency from the scheduler, not the tool.

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
- **`recommended_action`** — what to do, present **only when there is an
  action**. The field is **omitted entirely** when none is needed (the common
  healthy-node case), so treat its absence as "no action" and guard for it
  before reading into it — don't assume it's there. When present it is
  `{operational_state, diagnostic_action}` (e.g.
  `{"operational_state":"drain","diagnostic_action":"reboot"}`):
  `operational_state` is `monitor` or `drain` (never `none` — that case is the
  omitted field), and `diagnostic_action` (`reboot`, `reset_gpu`,
  `retest_hardware`, …) is present **only** when `operational_state` is `drain`.
  Map these to your fleet action and follow-up.
- **`affected_components`** / **`severity_by_domain`** — which domain and which
  `check_ids`/entities failed, so you route (GPU vs NVLink vs Xid) without
  re-parsing every result.
- **`check_results`** — per-check `severity`, `messages`, `observed_values`, and
  the resolved `thresholds` (with `overridden`), for the human-readable why.
- **`skipped_checks`** — checks that did **not** run, with a `skip_reason`
  (`THRESHOLD_UNCONFIGURED`, `CHECK_DISABLED`, `NO_APPLICABLE_MEASUREMENT`, …).
  A PASS with many skips is **partial coverage**, not a clean bill of health —
  check `skipped_checks` and `completion_ratio` if coverage matters to your gate.

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
   fleet," and "how coverage (`completion_ratio`) trends."

Because a verdict is a point-in-time snapshot, store each run rather than
overwriting — the history is what turns per-node gating into fleet insight.

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
