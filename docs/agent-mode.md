# Agent mode

> [!NOTE]
> Agentkit and its verdict API are experimental (v0), outside the project's
> support lifecycle. Health/readiness endpoints are a separate follow-up and
> are not provided by this API.

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

The default address is `127.0.0.1:8080`. The API is intended for callers local to
the node or its container environment; this version has no TLS, authentication,
or rate limiting. Choose the listener and container networking accordingly.

## Defaults and request overrides

| Setting | CLI default | Per-request behavior |
| --- | --- | --- |
| `--address` | `127.0.0.1:8080` | One listener for HTTP/1 and gRPC |
| `--node-id` | App-configured identifier, otherwise system hostname | Optional `node_id` replaces it for one request; whitespace-only values fail |
| `--cadence` | Omitted: all registered checks | Optional `cadence` selects `CONTINUOUS`, `PROLOG`, or `MAINTENANCE` |
| `--execution-context` | `TENANT_GUEST` | Optional `execution_context` selects `OPERATOR_INFRA`, `TENANT_GUEST`, or `OUT_OF_BAND` |
| `--config` | No overrides | Thresholds, disabled checks, collection timeout, and metrics are loaded once at startup |
| `--xid-from`, `--xid-to` | Current boot's Xid history | Process-wide journal bounds, using the same syntax as `run`; no API override |

Omitted request fields inherit the service defaults. Overrides do not change
later requests. Cadence is a nested window, Continuous ⊂ Prolog ⊂ Maintenance;
checks outside it appear in `skipped_checks` with `CADENCE_MISMATCH`. The YAML
configuration does not select cadence or execution context. Restart the service
to load a changed configuration file.

GET accepts only the three request fields as query parameters, once each.
POST accepts a JSON request body, without query overrides; `{}` and an empty
body both use defaults. Unknown fields, malformed values, duplicate query
parameters, and bodies larger than 1 MiB fail validation. Use the enum names
shown above.

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
| Caller canceled | CANCELED | 408, if the client can still receive it |
| Client or service request deadline expired | DEADLINE_EXCEEDED | 504 |
| Handler reports temporarily unavailable | UNAVAILABLE | 503 |
| Evaluation/conversion failed before a verdict could be delivered | INTERNAL | 500 |

HTTP error bodies use `{"error":"message"}`. The HTTP status and gRPC status
report whether a verdict was delivered; they do not encode its health severity.

One-shot mode retains its severity exit policy: PASS/INFO/WARNING → 0,
CRITICAL/UNKNOWN → 1, and runtime/output failure → 2. Agent mode exits 0 on
clean shutdown and 2 on startup or server failure. A request's verdict never
changes the running process's exit policy.

## Concurrency, deadlines, and shutdown

The service serializes evaluations because host NVML/DCGM sources are shared.
It does not coalesce requests or reuse a previous verdict. The configured
`collection_timeout_seconds` is the whole request budget, including time spent
waiting for another evaluation; the default is 60 seconds. A shorter client
deadline also applies. Each source retains its own watchdog within that budget.

Cancellation propagates to cooperative sensors. Native calls can outlive a
watchdog because they cannot be interrupted. The service tracks that work and
keeps admission closed until it returns, even after the canceled request has
received an error. Later callers can time out in the queue. This prevents an
unresponsive source from accumulating overlapping collections.

SIGINT or SIGTERM stops accepting requests and initiates shutdown. The transport
allows up to five seconds for active handlers before force-closing connections.
Backend cleanup must wait until underlying collection work is idle; an
uninterruptible native call may remain until process exit. Supervisors should
allow a shutdown interval longer than the transport grace period.

The service evaluates only on demand. It provides no polling schedule, streaming
telemetry, cross-node analysis, cached readiness, or remediation. A PASS verdict
is a point-in-time result; request another evaluation before the next dispatch.

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
journalctl_bin="$(command -v journalctl)"
journal_mounts=(-v "$journalctl_bin:/usr/bin/journalctl:ro")
while read -r dependency; do
  journal_mounts+=(-v "$dependency:$dependency:ro")
done < <(ldd "$journalctl_bin" | awk '$3 ~ /^\// && $1 !~ /^(lib(c|pthread|dl|rt|m)\.so)/ {print $3}')

docker run --rm --network host \
  --gpus all -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -v /run/log/journal:/run/log/journal:ro \
  -v /var/log/journal:/var/log/journal:ro \
  -v /etc/machine-id:/etc/machine-id:ro \
  "${journal_mounts[@]}" \
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
from the integration guide, change the container arguments to `serve`, and pick
an address reachable by the intended local caller. A sidecar shares its pod's
network namespace and can use loopback. No readiness or liveness endpoint is
provided; `/verdict` performs a full health evaluation.

## Run under systemd

Install the Linux binary at `/opt/health/health-validation`. If using a config,
create `/etc/health/overrides.yaml` first. For example:

```yaml
collection_timeout_seconds: 60
```

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
a handler that returns proto messages, keeping `agentkit/server` independent of
catalog and toolkit. `toolkit/verdictproto` is the conversion boundary; the
catalog and raw collection packages acquire no dependency on it.

The source of truth is the current `NodeVerdict` JSON schema **0.4**, including
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
| Evaluation | Fresh cycles and per-request cadence/context/node overrides; shared path parity with `run` |
| Cancellation | Queued deadlines, cooperative cancellation, and an abandoned native call blocking the next cycle |
| Metrics | Publish only after delivery; collector outage cannot change verdict or response |
| Platforms | Mock-backed tests on macOS; Linux builds/tests for cgo/NVML code and both supported CLI architectures |

Independent review should challenge the failure paths and test expectations,
including stale inventory, missing signals, unsupported enums, canceled queued
requests, disconnected clients, and shutdown while a source is still busy.
