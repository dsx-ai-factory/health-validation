# Verdict API (experimental)

[`health/v1/verdict.proto`](health/v1/verdict.proto) defines
`health.verdict.v1.VerdictService.Evaluate(EvaluateRequest) returns (NodeVerdict)`.
The generated Go package is
`github.com/dsx-ai-factory/health-validation/proto/go/health/v1`.
It is separate from the raw observation API in `collect/proto`.

Each request evaluates one node. Optional request fields select the scheduling
cadence, execution context, and node identifier; omission uses the server's
defaults. All verdict severities are successful responses. An inability to
produce a verdict is a transport error. HTTP clients use `GET /verdict` with
optional query parameters or `POST /verdict` with an `EvaluateRequest` JSON body.
See [agent operation](../docs/agent-mode.md) for request examples and error codes.

The fields follow the current catalog
[`NodeVerdict` schema](../catalog/health/node-verdict.schema.json), version **0.4**.
`schema_version` describes the verdict JSON contract; the `v1` protobuf namespace
versions the RPC and field-number contract. Clients must check `schema_version`
before acting. Preserve protobuf field numbers on additive changes; reserve
removed numbers and names. Breaking RPC or protobuf shape changes require a new
namespace. The API remains experimental alongside agentkit.

Enum names deliberately match catalog JSON, including lowercase category,
maturity, and operational-state names. Required scalar fields carry explicit
presence so `PASS`, `false`, and other zero values remain in JSON. Timestamps are
RFC3339 strings, retaining the catalog's original offset and nanosecond precision.
Remediation steps remain an open set of strings.

Generated protobuf clients preserve unknown numeric enum values; consumers must
treat these as unsupported output instead of interpreting their default value.
The Go converter rejects unknown enum values, unsupported schema versions, and
missing required scalar fields. This makes an unsupported response an error
rather than a healthy verdict.

Dynamic observations and threshold values use `google.protobuf.Value`. Conversion
preserves JSON meaning, including nested objects, arrays, and null, rather than
concrete Go runtime types. Numbers are accepted when their JSON numeric meaning
survives a `float64` round trip exactly; values that would change, including
`9007199254740993` and high-precision decimals, are rejected. Large integers and
finite floating-point tolerances remain valid when they meet that exactness test.
Protobuf repeated fields cannot distinguish nil from empty slices. The HTTP JSON
adapter uses proto field names and restores
the required empty `severity_by_domain` object, null `check_results`, and empty
component `check_ids` that standard protojson otherwise omits. Optional empty
collections stay omitted.

With [Buf](https://buf.build/docs/cli/) and the repository's Go toolchain installed,
regenerate the checked-in Go files from the repository root:

```sh
bash proto/hack/genproto.sh
```

The script installs pinned Go and gRPC generators in a temporary directory. The
contract is usable by any protobuf toolchain; it does not require catalog or
collector implementation code.
