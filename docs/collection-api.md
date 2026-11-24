# Collection and verdict APIs

> [!NOTE]
> `agentkit` remains experimental (v0), outside the project's support
> lifecycle. Its API may change.

Choose the result your integration needs:

| Entry point | Result | Evaluation |
| --- | --- | --- |
| `health-validation run` | JSON `NodeVerdict` on stdout | One complete node evaluation, then exit |
| `health-validation serve` | `NodeVerdict` over local gRPC or HTTP | One complete node evaluation per request |
| `health-validation collect` | JSON `ObservationBatch` on stdout | Raw observations only |
| `collect` Go library | Typed observations and inventory | Caller owns evaluation |

Use `GET/POST /verdict` or
`health.verdict.v1.VerdictService/Evaluate` for an evaluated verdict. The operator
`serve` command installs this verdict handler on the experimental transport,
which serves plaintext gRPC and HTTP/1 on one TCP port. See [Agent mode](/health-validation/operating/agent-mode) for the wire
contract, HTTP field names, request validation, and operating examples.

```shell
health-validation serve --address 127.0.0.1:8080
# From another terminal:
curl --fail http://127.0.0.1:8080/verdict
```

For raw evidence without a service, use:

```shell
health-validation collect --node-id node-a > observations.json
```

The raw observation contract remains in `collect/proto/health/v1/`; it does not
depend on catalog verdict types. Its JSON uses the standard protobuf mapping,
including lowerCamelCase names such as `collectionId`. The verdict endpoint
uses snake_case field names to match the one-shot verdict schema.

An optional `collect --config` file selects sensor domains through its check IDs.
See [Integrating into a GPU cluster](/health-validation/operating/integration)
for the sensor library, deployment resources, and separate evaluation of stored
observations.

## DCGM metrics, health, and inventory

Use `Source.Collect` for raw DCGM results or the `collect/sensors/dcgm` package
for observations. `New` defaults to all supported metrics when no IDs are given.
`NewWithHealth` collects only explicitly supplied metric IDs and enables health
when its systems mask is nonzero. With no metric IDs, a nonzero mask collects
health only. Both constructors support optional inventory. Reuse one source per
process and finish registration before the first metric or health read.
Inventory leaves registration open. Selected health collection reads the union
of systems registered on that source.

### Selecting outputs and identifying GPUs

`CollectionRequest` selects metric IDs, the registered health systems, and GPU
inventory independently. An empty request performs no native work. The source
looks up GPU UUIDs and PCI addresses before reading metric fields so available
identities stay with successful fields if a later read fails. Each entry in
`CollectionResult.Metrics` pairs the GPU's `Identity` with its `Fields`. Health
collection reads the native report first and then attempts identity lookup so a
lookup failure leaves the findings available. Inventory reuses metric discovery
when both are selected.

UUIDs identify GPUs across collections. PCI addresses locate GPUs on the node.
Either lookup can succeed independently. Metric targets retain a `sourceLocalId`
fallback when UUID lookup fails. Native numeric IDs may change after a hostengine
restart; consumers requiring persistent GPU identity must use UUIDs. When calling
`ReadGPUMetrics` or `GPUInventory` separately, pass IDs or identities from the
current session.

### Session recovery

Collections in different cycles can run concurrently and interleave native
operations. A restarted hostengine can assign the same numeric GPU ID to a
different device, so a collection uses one session for all reads. It binds on
its first native operation and keeps completed results if the session fails.
After connection loss, an unbound collection in a different cycle can reconnect.
Native work must finish before recovery can proceed, even after a timeout.
Recovery requires the source to own the process's sole go-dcgm client reference.
Another reference keeps the previous connection open.

An initialization failure remains with the collection that encountered it. A
different collection cycle can retry with the same registration. A failed health
setup can retry once any unfinished group cleanup succeeds. Metric field-mapping
errors leave health and inventory available. `WatchPolicy` errors detected at
construction affect the entire source, while invalid `HealthWatchOptions` affect
health registration.

### Health sampling and coverage

Set `HealthWatch` in `StandaloneOptions` or `EmbeddedOptions` to configure
health sampling separately from the metric `WatchPolicy`. Unset values request
a 30-second sampling interval and 10-minute retention. Use an interval suited
to your polling cadence and a `MaxKeepAge` that covers gaps between polls.
DCGM may enforce subsystem-specific minimum intervals and retention.
The hostengine shares samples per entity and field across watches. Other watches
can increase the effective sampling rate or retention for a shared field.

Health reads refresh discovery for each entity kind required by the requested
systems. Returning devices can rejoin an existing group while its current
members retain their history. GPU inventory can also include detached devices
unavailable to health watches.
`systemsWithoutTargets` identifies requested systems whose targets are absent.
`systemErrors` records discovery failures that cannot be attributed to a specific
device. `entityResults` groups each entity's identity, incidents, and
`collectionErrors`. Each collection error names the affected `systems` and keeps
its source status. Discovery and entity collection errors indicate incomplete
coverage and can accompany findings from usable members.
`groupMember` explicitly records whether the entity belongs to this read's native
group; it does not establish sample coverage. Rejected targets and entities
mentioned only in incidents also have results, with `groupMember: false`.
GPU UUIDs and PCI addresses are added when available; failed enrichment retains
the native ID and error. If every candidate is rejected, the report contains
these errors without a native overall result. If the incident limit is reached,
`possiblyTruncated` marks the result as potentially incomplete.

Incidents include `nativeErrorSeverity`, `nativeErrorCategory`, and `suggestion`
when the installed DCGM client library has metadata for the incident's code.
These are DCGM values, independent of the incident's native health result and
Health Validation verdict severity. Unknown codes retain their incidents with
those metadata fields absent.

The sensor emits one node-wide `dcgm.health` observation when health is selected.
Its report retains every incident under its entity and the native overall result
for the enabled group. Overall health, `monitoredSince`, and `possiblyTruncated`
describe the shared native group, rather than individual entity results.
The native overall result does not establish coverage for systems with
discovery or membership errors. If every requested system has no targets and
there are no incidents, the sensor emits a `NO_TARGETS` outcome without an
observation target. Consumers enabling health need a schema that supports
`DcgmHealthReport`.

DCGM can report incidents on the first check, and the source returns successful
native reports immediately. When a report has no incidents, some health rules
may still have had no usable samples. The report provides no per-rule readiness
signal.
`monitoredSince` records the group's first successful setup. It provides no
measure of history for individual rules or for members added later. For
example, a memory rule can need a week-old baseline. Consumers decide what
evidence they require before treating an absent incident as cleared.

## Experimental raw collection server

Existing raw clients can continue using `agentkit/server.New`, `Register`, and
`Serve`. That separate library path exposes `health.v1.CheckService/Check` and
`GET /check`, returning an `ObservationBatch` without catalog evaluation.
It is not registered by the operator `health-validation serve` command.

The `agentkit/examples/server-simple-collect` example defaults to port 18080:

```shell
curl --fail http://127.0.0.1:18080/check
```

The generated [API Reference](/health-validation/api-reference) documents this
raw `health.v1` contract. The
[Verdict API reference](/health-validation/reference/verdict-api) documents the
separate `health.verdict.v1` messages and RPC.
