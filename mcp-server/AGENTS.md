# AGENTS.md

Guidance for coding agents and people working in `mcp-server/`. The repository's
[AGENTS.md](../AGENTS.md) still applies; this file adds what is specific to the server.
[README.md](README.md) covers using the server.

## What this directory is

A read-only MCP server, in Go, for NVIDIA health-validation ("hv" below), in its own Go module
(`github.com/dsx-ai-factory/health-validation/mcp-server`) that builds against the checkout above it. It exposes hv's check catalog, compiled in,
and one verdict at a time from `health-validation serve` on the node's loopback address.

hv only judges a node and recommends; it never acts. This server has **no tool that changes anything**,
and must not get one: cordon, drain and GPU reset belong to the scheduler or a remediation system. Do
not add exec of the hv binary, NVML, DCGM or journal access here.

## How it works

```text
MCP client --stdio--> pkg/server --> pkg/catalogview --> hv catalog, compiled in (pure Go)
                      tools, gate,    list, describe,
                      limits, audit   remediation table
                          |
                          +--> pkg/hvclient --HTTP, loopback only--> health-validation serve
                          |    settings, decode,                     (GET /healthz, POST /verdict)
                          |    error kinds, owner check
                          +--> pkg/output: clean, cap, fence, redact
```

| Path | What it holds |
|---|---|
| `cmd/health-validation-mcp/main.go` | Flags (`-h`, `-v`, `-d`), the stderr logger, signals, shutdown |
| `pkg/server/server.go` | `Config` and `ConfigFromEnv` (`HV_MCP_ALLOW_EVALUATE`, `HV_MCP_EVALUATE_INTERVAL_SECONDS`), the call limiter, `register`, `schemaFor`, `callerError` |
| `pkg/server/tools.go` | The five tool literals, their schemas, `hv_service_status` |
| `pkg/server/verdict.go` | `hv_get_verdict`: single flight, the evaluate limiter, the audit line, `scheduling_advice`, coverage, findings |
| `pkg/hvclient/client.go` | Settings (`HV_MCP_SERVE_HOST`, `HV_MCP_SERVE_PORT`, `HV_MCP_SERVE_UID`, `HV_MCP_EVALUATE_TIMEOUT_SECONDS`) and the HTTP client: no proxy, no redirects, capped bodies |
| `pkg/hvclient/decode.go` | Strict verdict decode: unknown keys dropped, then schema `0.7` only, unknown enum names and numbers refused at any depth, hv's own wire checks, overall severity no lower than any check |
| `pkg/hvclient/errors.go` | Error kinds; a caller sees the kind and a hint, the log gets the detail |
| `pkg/hvclient/owner.go` | The optional socket-owner check through `/proc/net/tcp` |
| `pkg/catalogview/` | `List`, `Describe`, `Explain` over the compiled-in catalog. `enums_gen.go` is **generated** |
| `pkg/output/output.go` | `Clean`, `Cut`, `Fence`, `Entities`, `PIDs` and the sentences that replace tenant text |
| `docs/tools.md`, `docs/tools.json` | **Generated:** never edit them by hand |
| `docs/security.md` | Threat model; each control names the test that proves it |
| `docs/mcp-standards.md` | How the server compares with the MCP standards, and how to re-check |
| `lab/hvfixture/` | A fake serve for the e2e tests |
| `tests/e2e/` | e2e tests through the built binary (`-tags e2e`) |
| `docs/evals/evals.xml`, `tests/evals/` | Evaluation questions, and the test that checks their answers through the tools (`-tags evals`) |
| `docs/test-results/` | What the e2e runs returned, one directory per target |
| `skills/health-validation-mcp/` | How an agent should use the tools |

## Commands

```shell
make build lint test   # no node, no serve needed; run all three before a PR
make test-race-amd64   # the race detector in an emulated amd64 container, where the host kernel cannot run it
make docs              # regenerate enums_gen.go, docs/tools.md and docs/tools.json
make e2e               # the built binary over stdio against lab/hvfixture; rewrites docs/test-results/fixture
make image             # docker build, tagged health-validation-mcp:local
make e2e-image         # the fixture e2e with the server in its image (IMAGE=<ref>)
make e2e-serve         # the e2e against a real serve: HV_SERVE_PORT=<port> HV_HOST_LABEL=<label>
make evals             # check every answer in docs/evals/evals.xml through the built binary
```

`lab/hvfixture` runs health-validation's real verdict server and catalog over synthetic
measurements, so its results are not a real node's. `make scan` runs govulncheck, OSV, Trivy (telemetry off) and mcp-scanner; the tools must be installed.

## Rules that keep it safe

- **Read only.** Every tool sets `ReadOnlyHint: true`, `DestructiveHint: false`, `OpenWorldHint: false`.
  A tool that changes the node does not belong here.
- **Every argument is an enum or a bounded integer**, through `schemaFor`, which refuses unknown fields.
  Never add a free-form string, a node ID, a URL, a path or a host. The check ID enum comes from the
  catalog.
- **Write each tool as an `mcp.Tool` literal** with its name and all four annotation hints in it, so a
  scanner reading the source sees them. `register` adds only the call limit and the panic guard.
  `protocol.go` holds what goes around the SDK: the receiving middleware (null arguments, a panic guard for
  all of `tools/call`, `resources/*` and `prompts/*` not found) and `StdioTransport` (parse errors, no reply
  to a cancelled request).
- **Do not expose `node_id`, `execution_context` or `xid_to`** on `hv_get_verdict`. `OUT_OF_BAND` in
  particular would skip every check and read as a PASS.
- **Evaluation stays off unless `HV_MCP_ALLOW_EVALUATE` is exactly `true`.** Keep the single flight (a
  second call is refused, not queued), the evaluate limiter and the global call limiter. Never retry a
  call to serve.
- **Every evaluate attempt writes one audit line to stderr**, refusals included: `logEvaluate` in `verdict.go`, and `register` for a call refused by the global limit.
- **Serve's text is hostile.** Return it only through `output` (cleaned, capped, fenced with a marker per
  call). Check IDs and observed-value keys the compiled-in catalog does not name are fenced too. `gpu_compute_apps` messages carry tenant command lines and `gpu_xid_*` messages carry journal
  text: return only counts, PIDs and the compiled-in catalog's own text for those. Never return `node_id`.
- **A bad verdict is never a pass.** Anything `DecodeVerdict` refuses becomes `NO_VERDICT`. A PASS
  where no GPU or NVLink check ran is `READY_LOW_COVERAGE`, whatever the Xid count: Xid checks only read
  the journal.
- **Raw errors go to stderr only.** Callers get `hvclient` error kinds through `callerError`.
- **The target is fixed:** `127.0.0.1` or `::1`, no proxy, no redirects. No tool argument can change it.
- **stdout belongs to MCP.** Log to stderr.
- **Only claim what a test proves.** `docs/security.md` lists a control only with a test that enforces it.

## How to add things

**A tool**

1. Add the logic to `pkg/catalogview` (no node) or `pkg/hvclient` (serve), with a test.
2. Add an `mcp.Tool` literal in `register` in `pkg/server/tools.go`, with a name starting `hv_`, a
   title, all four hints, a description that says what it reads and returns and when to use a sibling,
   and a schema from `schemaFor`.
3. Update `TestEvaluateIsOffByDefault` and `TestEveryToolDeclaresAllFourHints`, and send hostile values
   through `TestBadArgumentsNeverReachTheService` if it takes arguments.
4. Add a row to the README's tool table (`TestTheReadmeListsEveryToolAndSetting` checks it), then run
   `make docs`.
5. If it adds a control or a risk, update `docs/security.md`.

**A setting:** add an `Env...` constant and read it in `ConfigFromEnv` in `pkg/hvclient/client.go` or
`pkg/server/server.go`. A bad value must stop the server at start. Add it to `usage` in `main.go`, the
README's settings table (`TestTheReadmeListsEveryToolAndSetting` checks it).

**A catalog or schema change in hv:** run `make docs`, and read what `enums_gen.go` and
`docs/tools.md` gained. If the verdict schema moved past `0.7`, update `pkg/hvclient/decode.go` and its
tests on purpose.

**A remediation step:** hv defines them. `TestEveryActionHasAMeaning` fails until
`pkg/catalogview/remediation.go` explains the new one.

## Commits and PRs

Follow the repository's [CONTRIBUTING.md](../CONTRIBUTING.md): Conventional Commits with the
`mcp-server` scope, DCO sign-off and signed commits. Run `make build lint test e2e` from this directory
before a PR, since the repository's Bazel CI does not build this module. Say what was not tested.
Never write a host name, node ID or internal domain here or on GitHub: say "the build machine".
