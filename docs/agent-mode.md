# Agent mode

> [!NOTE]
> Agentkit and its verdict API are experimental (v0), outside the project's
> support lifecycle. A liveness signal for stuck native calls is provided (see
> [Liveness](#liveness)); readiness endpoints are a separate follow-up and are
> not provided by this API.

`health-validation serve` keeps the node's collectors available and evaluates
health on request. Every request starts a fresh collection cycle and runs the
same evaluation core as `health-validation run`. It returns one node's verdict;
your caller decides when to request it and what action to take.

## Start and call the service

On a Linux GPU node with the same NVML, sysfs, and journal access needed by `run`:

```shell
health-validation serve --address 127.0.0.1:8080 --execution-context OPERATOR_INFRA
```

From another terminal on that node:

```shell
curl --fail http://127.0.0.1:8080/verdict
curl --fail 'http://127.0.0.1:8080/verdict?cadence=PROLOG&node_id=node-a'
curl --fail -X POST http://127.0.0.1:8080/verdict \
  -H 'Content-Type: application/json' \
  -d '{"cadence":"CONTINUOUS","execution_context":"TENANT_GUEST","node_id":"node-a"}'
```

HTTP/1 and plaintext gRPC share the address. gRPC clients call
`health.verdict.v1.VerdictService/Evaluate` with an `EvaluateRequest` and receive
a `NodeVerdict`. Generate clients from
[`proto/health/v1/verdict.proto`](https://github.com/dsx-ai-factory/health-validation/blob/main/proto/health/v1/verdict.proto).
Generated Go types are in
`github.com/dsx-ai-factory/health-validation/proto/go/health/v1`.

The default address is `127.0.0.1:8080`. The listener requires a numeric loopback
IP such as `127.0.0.1` or `::1`. Wildcard addresses, remote IPs, and hostnames
are rejected. Callers must share the service's network namespace. This version
has no TLS, authentication, or per-client rate limiting; every caller that can reach the
listener must be trusted.

## Defaults and request overrides

| Setting | CLI default | Per-request behavior |
| --- | --- | --- |
| `--address` | `127.0.0.1:8080` | One listener for HTTP/1 and gRPC |
| `--request-timeout` | `2m` | Admission and evaluation budget; must exceed `collection_timeout_seconds` |
| `--stuck-call-deadline` | `10m` | Liveness fails after a source call runs this long; must exceed `--request-timeout` |
| `--node-id` | App-configured identifier, otherwise system hostname | Optional `node_id` replaces it for one request; whitespace-only values fail |
| `--cadence` | Omitted: all registered checks | Optional `cadence` selects `CONTINUOUS`, `PROLOG`, or `MAINTENANCE` |
| `--execution-context` | `TENANT_GUEST` | Optional `execution_context` selects `OPERATOR_INFRA`, `TENANT_GUEST`, or `OUT_OF_BAND` |
| `--config` | No overrides | Thresholds, disabled checks, collection timeout, and metrics are loaded once at startup |
| `--xid-from`, `--xid-to` | Current boot's Xid history | Optional `xid_from` / `xid_to` replace the node-wide Unix-second bounds for one request |

Omitted request fields inherit the service defaults. Overrides do not change
later requests. Cadence is a nested window, Continuous ⊂ Prolog ⊂ Maintenance;
checks outside it appear in `skipped_checks` with `CADENCE_MISMATCH`. The YAML
configuration does not select cadence or execution context. Restart the service
to load a changed configuration file.

Configured threshold overrides are checked for wire compatibility before the
listener starts. A numeric value that would be rounded by the verdict API is
a configuration error (exit 2). Overrides are recorded in the system journal once
at startup, including overrides on disabled checks, without GPU discovery.
Audit `ORIGINAL_VALUE` uses the architecture-independent catalog default, or
`null` where only architecture-specific defaults exist. Each request's verdict
still reports its architecture-specific `thresholds[].original_value`.
If the audit write fails, the listener does not start and the process exits 2.
With no threshold overrides, startup skips the audit journal. Requests do not
repeat the audit. GPU discovery runs per request, so the service can start
before the driver is ready and report discovery failures as verdicts.

`xid_from` is non-negative; `xid_to` is positive. The effective start must be
less than the effective end. The window is (`xid_from`, `xid_to`], matching the CLI, and each
omitted field inherits its startup value. Per-GPU `--xid-from PCI_ADDRESS=SECONDS`
settings retain precedence over the node-wide start. Zero is an explicit start
at the Unix epoch, not omission. Requests cannot clear an inherited end bound.
The scan still reads only the current boot.
If a custom sensor supplies the Xid signals, bounded requests use that sensor's
time bounds support. A sensor without that support causes HTTP 400 or gRPC
`INVALID_ARGUMENT` for requests that specify bounds.

For example, move the Xid window for one evaluation:

```shell
curl 'http://127.0.0.1:8080/verdict?xid_from=1730000000&xid_to=1730003600'
curl -X POST http://127.0.0.1:8080/verdict \
  -H 'Content-Type: application/json' \
  -d '{"xid_from":"1730000000","xid_to":"1730003600"}'
```

GET accepts all five request fields as query parameters, once each.
POST accepts a JSON request body, without query overrides; `{}` and an empty
body both use defaults. Unknown fields, malformed values, duplicate query
parameters, and bodies larger than 1 MiB fail validation. Use the enum names
shown above. JSON accepts quoted decimal strings for the 64-bit timestamp
fields (the protobuf JSON convention), or exact integer numbers.

## Verdicts and transport errors

A produced verdict of **any severity** returns gRPC OK / HTTP 200. A successful
HTTP call can contain `overall_severity: CRITICAL` or `UNKNOWN`; read that field
and `skipped_checks` before making a scheduling decision. An architecture read
that fails without exhausting the request budget produces the same CRITICAL
`architecture_discovery` verdict as one-shot mode.

| Outcome | gRPC | HTTP |
| --- | --- | --- |
| Verdict produced, including a discovery-failure verdict | OK | 200 |
| Invalid request override or JSON | INVALID_ARGUMENT | 400 |
| Request capacity exhausted | RESOURCE_EXHAUSTED | 429 |
| Caller canceled | CANCELED | 408, if the client can still receive it |
| Service reports a request deadline expired | DEADLINE_EXCEEDED | 504 |
| Handler reports temporarily unavailable | UNAVAILABLE | 503 |
| Evaluation/conversion failed before a verdict could be delivered | INTERNAL | 500 |

HTTP error bodies use `{"error":"message"}`. The HTTP status and gRPC status
report whether a verdict was delivered; they do not encode its health severity.
HTTP clients must also handle their own timeout errors: a client deadline may
cancel the request without receiving an HTTP 504 response.

One-shot mode retains its severity exit policy: PASS/INFO/WARNING → 0,
CRITICAL/UNKNOWN → 1, and runtime/output failure → 2. Agent mode exits 0 on
clean shutdown and 2 on startup or server failure. A request's verdict never
changes the running process's exit policy.

## Concurrency, deadlines, and shutdown

The service serializes evaluations because host NVML/DCGM sources are shared.
After each evaluation, it waits for all underlying native calls to finish, then
shuts down its NVML instance before admitting another collection. Successful
cleanup releases the service's GPU device handles while idle; the next request
opens a fresh instance, inventory reader, and sensor. A cleanup failure is
logged and ownership is retained for a later retry. Other GPU clients, including
nv-hostengine, still need to release their own handles before a GPU reset.

A DCGM `DCGM_ST_CONNECTION_NOT_VALID` collection failure marks that client
session stale. The failure remains in the current verdict; the next collection
closes the old session and creates a new client and field watches. Recovery
waits for unfinished native calls, and healthy sessions remain reusable.

It does not coalesce requests or reuse a previous verdict. A shared limit admits
at most 32 requests across HTTP and gRPC, including calls waiting for collection,
response delivery, and metrics publication. Excess calls fail promptly with
HTTP 429 / gRPC RESOURCE_EXHAUSTED and do not collect or publish metrics. HTTP
admission occurs before body decoding; gRPC admission occurs after message
decoding, before evaluation. This limits admitted work, not open connections.
Library hosts can set a positive limit with `WithMaxInFlightRequests`.
Retry overloaded requests with backoff.
`collection_timeout_seconds` bounds each source call independently (default
60 seconds). `--request-timeout` separately bounds admission and evaluation,
including time spent waiting for another evaluation (default 2 minutes, matching
the raw Check API's HTTP write timeout).
It must exceed the source timeout; allow enough time for sequential source
calls and verdict production. This lets a source watchdog produce a failure
verdict before the request expires. A shorter client deadline also applies.
Protocol classification has a ten-second read and write budget, cleared before
HTTP or gRPC handles the connection.
HTTP request decoding and response writing are outside the request budget,
so the full HTTP call can take longer. HTTP response delivery has a separate
two-minute write deadline that starts after evaluation returns, covering both
error responses and successful verdict responses (including encoding). This
write deadline applies only to HTTP; the server does not enforce a delivery
deadline for gRPC. gRPC callers must set their own client deadlines to bound
the full RPC, including response delivery.

Cancellation propagates to cooperative sensors. Native calls can outlive a
watchdog because they cannot be interrupted. The service tracks that work and
keeps admission closed until it returns, even after the canceled request has
received an error. Client cancellation or a client deadline alone does not
produce a busy-source verdict: subsequent callers keep waiting within their own
request budgets. The source's original watchdog continues independently. If
the call finishes before that watchdog expires, collection resumes normally.
If the source watchdog expires while work is unfinished, queued and new callers
receive an UNKNOWN `collection_source_busy` verdict naming its source
and call start time, with `retry_collection`. These verdicts use the normal
post-response metrics path. Fresh collection resumes when all underlying calls
return. This prevents an unresponsive source from accumulating overlapping
collections. NVML release between evaluations uses the same watchdog budget.
If release stalls, the busy verdict names `nvml.shutdown`.

SIGINT or SIGTERM stops accepting requests and initiates shutdown. The transport
allows up to five seconds for active handlers before force-closing connections.
Backend cleanup must wait until underlying collection work is idle; an
uninterruptible native call may remain until process exit. Supervisors should
allow a shutdown interval longer than the transport grace period.

The service evaluates only on demand. It provides no polling schedule, streaming
telemetry, cross-node analysis, cached readiness, or remediation. A PASS verdict
is a point-in-time result; request another evaluation before the next dispatch.

## Liveness

A native DCGM or NVML call that never returns keeps admission closed, so every
request gets `collection_source_busy` until the process restarts. The process
still accepts connections, so a TCP probe passes. `serve` reports this state
through two liveness endpoints on the same port:

- `GET /healthz` returns 200 `{"status":"ok"}`, or 503
  `{"status":"unhealthy","stuck_calls":[{"source":"…","since":"…"}]}`.
- The standard `grpc.health.v1.Health` service (service name `""`). `Check`
  returns SERVING or NOT_SERVING; `Watch` polls about once a second and sends on
  change. Other service names return NOT_FOUND.

Liveness is unhealthy once a call has been unfinished past its source watchdog
and has run longer than `--stuck-call-deadline`, measured from the call's start.
It recovers by itself when the call returns; there is no latch. It is healthy
while idle. Probes never take the admission gate and are exempt from the
in-flight request limit, so they answer during overload (no 429). One warning is
logged per transition, not per probe.

This is liveness only. A healthy response says the process is not wedged behind
a native call, not that the node is healthy or that a verdict is ready. Restart
policy belongs to the supervisor: set the deadline well above `--request-timeout`
and add restart-loop protection, since a deadline set too low restarts a slow
but working call. For systemd, `serve` does not exit on its own; use a
watchdog or a probe script that curls `/healthz`.

## Metrics and logging

When `metrics.enabled` is configured, the agent attempts to publish each
successfully delivered verdict after its response has been written. HTTP waits
for the body write and flush; gRPC uses successful RPC completion. A failed
response does not publish a success metric.

Publishing is best-effort. Collector failures are logged and cannot change the
response, verdict, or exit code. The publisher uses one ManualReader export,
no retries, and the configured metrics timeout. Metrics use their own bounded
context so a caller disconnecting after delivery does not cancel publication.
Logs include request correlation, node identifier, duration, and successful
verdict severity. The default `--verbose` flag enables additional diagnostics.

## Run in a container

Use a build/image containing `serve`. The image has the binary as its entry
point; its GPU and host-journal requirements are the same as `run`. The following
Bash example reuses the explicit journal mounts described in
[Integrating into a GPU cluster](/health-validation/operating/integration):

```bash
image='ghcr.io/dsx-ai-factory/health-validation:<tag>' # replace <tag>
dcgm_lib='/path/to/libdcgm.so.4' # replace with the host DCGM 4 client library path
journalctl_bin="$(command -v journalctl)"
mounts=()
declare -A mounted=()
mount_ro() {
  local source="$1" destination="$2"
  if [[ -z ${mounted[$destination]:-} ]]; then
    mounted[$destination]=1
    mounts+=(-v "$source:$destination:ro")
  fi
}

mount_ro "$journalctl_bin" /usr/bin/journalctl
mount_ro "$dcgm_lib" "$dcgm_lib"
for binary in "$journalctl_bin" "$dcgm_lib"; do
  while read -r dependency; do
    mount_ro "$dependency" "$dependency"
  done < <(ldd "$binary" | awk '$3 ~ /^\// && $1 !~ /^(lib(c|pthread|dl|rt|m)\.so)/ {print $3}')
done

docker run --rm --network host --pid host \
  --gpus all -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -e LD_LIBRARY_PATH="$(dirname "$dcgm_lib")" \
  -v /run/log/journal:/run/log/journal:ro \
  -v /var/log/journal:/var/log/journal:ro \
  -v /etc/machine-id:/etc/machine-id:ro \
  "${mounts[@]}" \
  "$image" serve --address 127.0.0.1:8080 \
  --node-id "$(hostname)" --execution-context OPERATOR_INFRA
```

This example uses Linux host networking so the container's loopback listener is
available from the node. The host's `journalctl` must have no unresolved shared
libraries and must be compatible with the image's glibc. Keep all node GPUs
visible; exposing a subset changes the inventory that `gpu_count` evaluates.
The one-shot `scripts/health-validation-run.sh` wrapper inserts `run` and is not
a `serve` launcher.

For Kubernetes, reuse the GPU allocation and journal mounts or journalctl shim
from the integration guide and change the container arguments to `serve`.
The caller must share the service's network namespace; a sidecar in the same
pod can use loopback. Use `/healthz` for a liveness probe (see
[Liveness](#liveness)). No readiness endpoint is provided; `/verdict` performs a
full health evaluation, so do not use it as a probe.

```yaml
livenessProbe:
  httpGet: {path: /healthz, port: 8080, host: 127.0.0.1}
  periodSeconds: 30
  failureThreshold: 3
```

The kubelet's `httpGet` probe connects from the node, so it reaches the
loopback listener only with `hostNetwork: true`. Otherwise use an `exec` probe
that requests `/healthz` from inside the pod's network namespace.

## Run under systemd

Install the Linux binary at `/opt/health/health-validation`. If using a config,
create `/etc/health/overrides.yaml` first. For example:

```yaml
collection_timeout_seconds: 60
```

The unit below requires `/etc/health/overrides.yaml` to exist. To use the
built-in defaults without a config file, remove
`--config /etc/health/overrides.yaml` from `ExecStart`.

Install this unit as `/etc/systemd/system/health-validation.service`, then use
the normal systemd daemon-reload and start commands:

```ini
[Unit]
Description=On-demand node health verdict service
After=network.target

[Service]
Type=simple
ExecStart=/opt/health/health-validation serve --address 127.0.0.1:8080 --config /etc/health/overrides.yaml --execution-context OPERATOR_INFRA
Restart=on-failure
TimeoutStopSec=10

[Install]
WantedBy=multi-user.target
```

Run with permissions to read the node's GPU and current-boot kernel journal.
The process writes diagnostics to stderr for the service journal; verdicts are
returned to callers. Systemd restart policies act on service exit, so a CRITICAL
verdict alone does not restart the service.

## Design and compatibility

`toolkit/app` owns configuration and backend lifetime. Its shared evaluation
core owns a fresh cycle, architecture discovery, runnable-check selection,
sensor collection, and verdict aggregation for each call. The transport accepts
a handler that returns proto messages, keeping `agentkit/verdictserver` independent of
catalog and toolkit. `toolkit/verdictproto` is the conversion boundary; the
catalog and raw collection packages acquire no dependency on it.

The source of truth is the current `NodeVerdict` JSON schema **0.7**, including
ordered remediation actions. The proto package is `health.verdict.v1`; its major
version identifies the wire API, while `schema_version` identifies the verdict
content. Keep additive fields compatible within the wire API and never reuse
field numbers. Breaking wire changes require a new proto package version.
The schema parity test must fail when the catalog gains an unmatched field.

HTTP uses protobuf JSON with proto field names (snake_case), plus canonical
required empty containers. Scalar presence preserves values such as PASS,
`skipped: false`, and `nvidia_endorsed: false`; timestamp strings retain offsets
and nanosecond precision. JSON whitespace and field order are not significant.

`observed_values` and threshold values use `google.protobuf.Value`. Conversion
preserves JSON values, including nested objects and lists, rather than their
original Go runtime types. Non-finite numbers and numbers whose JSON numeric
meaning changes through protobuf's double representation are rejected rather
than silently rounded. This includes some large integers and high-precision
decimals. Consumers must treat unknown schema
versions or enum values as unsupported, rather than assuming PASS. The Go
converter rejects unknown numeric enums and missing required scalar fields.

The operator `serve` command registers the verdict API. The separate
experimental raw `server.New` / `Register` / `Serve` library path retains
`CheckService.Check` and `GET /check`; raw observations also remain available
through `health-validation collect` and the `collect` library. Regenerate checked-in verdict Go code with
`bash proto/hack/genproto.sh`; the script requires buf and installs pinned Go
protoc plugins.

## Verification matrix

| Area | Required evidence |
| --- | --- |
| Existing CLI behavior | Unchanged one-shot tests, output encoding, severity exits, discovery-failure and metrics ordering |
| Contract | Schema field parity, exhaustive enum mapping, required scalar presence, nested value and number boundaries |
| Converter | PASS, CRITICAL, skipped, and discovery verdict round trips; canonical JSON validated against the schema |
| Transport | HTTP and gRPC success/error paths on one port, strict request parsing, size limits, graceful shutdown |
| Evaluation | Fresh cycles and per-request cadence/context/node/Xid-window overrides; shared path parity with `run` |
| Cancellation | Queued deadlines, cooperative cancellation, and an abandoned native call blocking the next cycle |
| Metrics | Publish only after delivery; collector outage cannot change verdict or response |
| Platforms | Mock-backed tests on macOS; Linux builds/tests for cgo/NVML code and both supported CLI architectures |

Independent review should challenge the failure paths and test expectations,
including stale inventory, missing signals, unsupported enums, canceled queued
requests, disconnected clients, and shutdown while a source is still busy.
