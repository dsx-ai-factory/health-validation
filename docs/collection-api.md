# Collection and verdict APIs

> [!NOTE]
> `collect` and `agentkit` remain experimental (v0), outside the project's support
> lifecycle. Their APIs may change.

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
