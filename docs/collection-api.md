# Collection API

> [!NOTE]
> The collection API is experimental (v0). It is part of the `agentkit` package,
> which is outside the project's support lifecycle, and it may change.

`health-validation run` returns a verdict. The collection API returns the raw
measurements instead: one collection cycle per request, with no checks
evaluated. Use it when an agent or service needs node-local evidence and wants to
evaluate it elsewhere.

The `agentkit` server exposes the same call over gRPC and HTTP on a single
address. Both return an `ObservationBatch`. The server is plaintext only and has
no TLS.

| Transport | Call | Details |
|---|---|---|
| gRPC | `health.v1.CheckService/Check` | Unary. See the [API Reference](/health-validation/api-reference/health-v-1/check-service/health-v-1-check). |
| HTTP | `GET /check` | Returns the batch as JSON, or a `500` with the error text if collection fails. |

```shell
curl http://127.0.0.1:18080/check
```

The address above is the default of the `server-simple-collect` example in
`agentkit/examples/`. `agentkit` is a library, so you run your own binary built
on `server.Serve`, and you choose the address and which sensors to register.

## Differences from the API Reference

The API Reference is generated from `collect/proto` and documents the gRPC
call. Two things differ from what the HTTP endpoint returns:

- **Field names.** HTTP responses use the standard protobuf JSON mapping, so
  names are lowerCamelCase (`collectionId`, `gpuSnapshots`). The reference
  example shows the proto names (`collection_id`, `gpu_snapshots`).
- **Optional fields.** The reference example wraps proto3 `optional` fields in an
  extra object, such as `_brand`. In real JSON the field appears directly, as
  `brand`.

## The same data from the CLI

`health-validation collect` writes the same `ObservationBatch` as JSON to stdout
without starting a server. See [Integrating into a GPU
cluster](/health-validation/operating/integration) for how to ship it.
