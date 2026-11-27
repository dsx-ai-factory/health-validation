# health-validation-mcp

A read-only MCP server for health-validation. An MCP client such as Claude Code can browse the check
catalog, ask what a check or a remediation step means, and, when the operator allows it, ask the
node's `health-validation serve` process for one verdict.

health-validation only judges a node and recommends. It never acts on it, and neither does this
server: it has no tool that changes anything. Cordon, drain and GPU reset belong to the scheduler or
to a remediation system.

This directory is its own Go module, built with `go` against the checkout above it. Nothing that
health-validation ships links it, and it adds the Go MCP SDK to this module only.

## How it works

```text
MCP client --stdio--> health-validation-mcp --HTTP, loopback only--> health-validation serve --> GPU, DCGM, journal
                      |
                      +-- the health-validation catalog, compiled in (no node access)
```

- The catalog tools read the pure-Go `catalog` package, compiled into this binary. They never touch
  the node, so they work anywhere, a laptop included.
- `hv_service_status` and `hv_get_verdict` call `health-validation serve`, which the operator already
  runs on the node on a loopback address. This server holds no GPU, journal or DCGM access, builds with
  `CGO_ENABLED=0`, and runs as a non-root user.
- `hv_get_verdict` is off unless `HV_MCP_ALLOW_EVALUATE=true`, as each call starts real collection on
  the node.

## Tools

| Tool | Reads the node | What it does |
|---|---|---|
| `hv_service_status` | serve's `/healthz` only | Whether serve answers, stuck source calls, whether evaluation is on and when it may next run, the catalog and schema versions. Liveness is not node health |
| `hv_list_checks` | no | The compiled-in catalog, 25 checks per page, filtered by category, architecture, cadence and maturity |
| `hv_describe_check` | no | One check's signals, rules, thresholds, dependencies and recommended action |
| `hv_explain_remediation` | no | What a remediation step means, and which checks recommend it |
| `hv_get_verdict` | yes, through serve | One evaluation: `scheduling_advice`, severity, node state, ordered remediation steps, and a coverage block. Only with `HV_MCP_ALLOW_EVALUATE=true` |

Every argument is an enum or a bounded integer. `hv_get_verdict` takes `cadence`, `detail` and
`xid_lookback_minutes`; it does not take a node ID, an execution context or an Xid end time, so serve's
own defaults apply.

**Read `scheduling_advice` and `coverage`, not only `overall_severity`.** A `PASS` where no GPU or NVLink
check ran, as on a GPU that health-validation does not know, comes back as `READY_LOW_COVERAGE`. Xid
checks do not count: they only read the journal. The values are `READY`, `READY_LOW_COVERAGE`,
`MONITOR`, `DRAIN`, `RETRY_COLLECTION` and `NO_VERDICT`.

Every tool's inputs, outputs and annotations are in [docs/tools.md](docs/tools.md), generated from what
the server returns for `tools/list`, as is the raw [docs/tools.json](docs/tools.json). A test fails if
they no longer match the server; `make docs` regenerates them, and the catalog enums with them.

## Build and run

With Go 1.27.3 or later, from this directory:

```shell
make build    # writes bin/health-validation-mcp
make test     # unit and MCP tests, no node and no serve
make e2e      # the built binary over stdio against lab/hvfixture
```

`go install ...@version` does not work for this module, because its `go.mod` points at the checkout
above it.

`health-validation-mcp --version` prints the version and the catalog version. Run with no flags, it logs
one line to stderr and then waits for an MCP client on stdin, so it looks idle: that is normal, and
Ctrl-C stops it. `--help` lists the flags and settings, and `--debug` logs each call to serve on stderr:
method, path, HTTP status, duration and error kind, never the body.

**Add it to your client.** For Claude Code, catalog tools only:

```shell
claude mcp add health-validation -- /path/to/health-validation-mcp
```

With evaluation on, on a node that runs serve:

```shell
claude mcp add health-validation -e HV_MCP_ALLOW_EVALUATE=true -- /path/to/health-validation-mcp
```

Other clients take the same command in their MCP config:

```json
{ "mcpServers": { "health-validation": { "command": "/path/to/health-validation-mcp" } } }
```

**The image.** `make image` builds `health-validation-mcp:local`, with the repository root as the build
context: one static binary on distroless, user 65532. It must share serve's network namespace to reach
its loopback address:

```shell
docker run --rm -i --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --memory=128m --cpus=1 --pids-limit=64 --network=host \
  -e HV_MCP_ALLOW_EVALUATE=true \
  health-validation-mcp:local
```

Use `--network=container:<serve container>` instead of `--network=host` when serve runs in a container
without host networking. It needs no devices, mounts or journal.

**Optional: the skill** in [skills/health-validation-mcp](skills/health-validation-mcp/SKILL.md) tells
an agent how to read verdicts safely. It holds only instructions; copy it into your agent's skills
folder.

## Run health-validation serve beside it

`hv_service_status` and `hv_get_verdict` need `health-validation serve` on the same host, on a numeric
loopback address, run as [docs/agent-mode.md](../docs/agent-mode.md) says, with the GPU and journal access
it needs:

```shell
health-validation serve --address 127.0.0.1:8080 --execution-context OPERATOR_INFRA
```

- The port must match `HV_MCP_SERVE_PORT` (default 8080). For `--address [::1]:8080`, set
  `HV_MCP_SERVE_HOST=::1`.
- This server never sends an execution context, so serve's `--execution-context` decides which checks
  run. Serve's default is `TENANT_GUEST`.
- Serve's `--config` (disabled checks, threshold overrides) applies to every verdict. The catalog tools
  show the compiled-in defaults, not that config.
- Serve's default `--request-timeout` is 2 minutes. `HV_MCP_EVALUATE_TIMEOUT_SECONDS` (default 150) should
  stay above it, or this server gives up first and reports `timeout`.
- Serve has no authentication: any local process can call it, and could also listen on its port if serve
  is down. Set `HV_MCP_SERVE_UID` to the user serve runs as, and this server checks, on Linux, that the
  listening socket and serve's end of each connection belong to that user before a request goes over it.
- In Kubernetes, run this server as a sidecar in serve's pod, so both share loopback.

Build serve and this server from the same commit, and build this one with `HV_VERSION` set to the
version serve reports (`make build HV_VERSION=v0.4.0` beside a v0.4.0 serve; plain builds of both say
`dev`). If the versions differ, a verdict says `version_skew: true`, and the catalog tools may describe
checks that serve does not run.

## Settings

| Variable | Default | What it does |
|---|---|---|
| `HV_MCP_SERVE_HOST` | `127.0.0.1` | Where serve listens: exactly `127.0.0.1` or `::1` |
| `HV_MCP_SERVE_PORT` | `8080` | Serve's port, 1024 to 65535 |
| `HV_MCP_SERVE_UID` | off | If set, the numeric user ID that must own serve's listening socket. Linux only |
| `HV_MCP_ALLOW_EVALUATE` | off | Exactly `true` adds `hv_get_verdict`. Any other value leaves it off and is logged |
| `HV_MCP_EVALUATE_INTERVAL_SECONDS` | `10` | At most one evaluation per this many seconds, 5 to 600 |
| `HV_MCP_EVALUATE_TIMEOUT_SECONDS` | `150` | How long to wait for a verdict, 10 to 900 |

A bad value for any setting but `HV_MCP_ALLOW_EVALUATE` stops the server at start. A second evaluation
while one runs is refused, not queued, and all tool calls share a limit of 10 per second, burst 20.
Nothing is retried: a busy serve (HTTP 429) is reported as `busy`.

## Test results

[docs/test-results/fixture](docs/test-results/fixture) holds what the built binary returned over stdio
in `make e2e`, one file per case: a healthy GB200, NVLink down, Xid 79, an unknown architecture, a
threshold override, a discovery failure, a busy source, serve stopped or killed mid-call, a refused
second call, a refused port owner, and evaluation left off. **These come from synthetic measurements:**
`lab/hvfixture` runs health-validation's real verdict server and catalog, but feeds it made-up readings,
not a GPU.

`make e2e-serve HV_SERVE_PORT=<port>` runs the same e2e against your own `health-validation serve` on
the same host, and writes what it returned to `bin/test-results/serve` (set `SERVE_RESULTS` to change
it). Set `HV_SERVE_HOST=::1` if serve listens on `[::1]`, and `HV_SERVE_UID` to the user serve runs as
to test the owner check too. Build the server with
`HV_VERSION` set to serve's version, or the run fails on version skew. The fixture-only cases (cancel,
kill mid-call, second call, owner refusal) are skipped against a real serve.

[docs/evals/evals.xml](docs/evals/evals.xml) holds 10 questions an operator might ask, each needing
several tool calls, with one checkable answer each. `make evals` works out every answer through the
built binary and fails if one is wrong. No model has been run against them yet.

## Standards and security

The threat model and each control, with the test that proves it, are in
[docs/security.md](docs/security.md). Each MCP requirement, its status and the known gaps are in
[docs/mcp-standards.md](docs/mcp-standards.md). It speaks MCP 2026-07-28 through go-sdk v1.8.0, and
accepts verdict schema `0.7` only; any other version gives `NO_VERDICT`. Report a vulnerability as the
repository's [SECURITY.md](../SECURITY.md) says.

`make scan` runs govulncheck, OSV-scanner, Trivy (with telemetry off) on the image, and Cisco
`mcp-scanner` (YARA and schema, offline). The tools must be installed, and no CI runs them, so run
`make scan` before a change that touches dependencies or tools. govulncheck lists one cel-go advisory
in code this server never calls.

## What is not tested

- **A real serve on a GPU health-validation knows.** No GPU check has been proven through this server
  against a real serve on a supported GPU.
- **Any architecture-scoped check**, DCGM signals, NVLink, InfiniBand and P2P checks, real Xid detection
  from a kernel journal, or a hardware-fault CRITICAL from a real node.
- **A real `collection_source_busy`** verdict, load across a fleet, or a real HTTP 429 from serve.
- **CI:** no pipeline here builds or tests this module yet.
- **Evaluations run by a model.** `make evals` checks the answers mechanically.
