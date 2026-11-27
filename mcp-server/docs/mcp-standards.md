# How this server compares with the MCP standards

Checked on 2026-11-26 against spec revision **2026-07-28**, with go-sdk v1.8.0. To check again, start at
[What to re-check](#what-to-re-check). "Tested" names the test that proves it. Tests marked (e2e) are in
`tests/e2e`: they run the built binary on its real stdio against `lab/hvfixture`, health-validation's real
verdict server over synthetic measurements ([results](test-results/fixture)). The others are unit or
in-process tests. `make e2e-serve` ran against a real `health-validation serve` on the build machine and
the Pi ([build machine](test-results/build-machine), [Pi](test-results/pi5)).

## What the standards are

| Standard | Covers | Applies here |
|---|---|---|
| [MCP specification](https://modelcontextprotocol.io/specification/latest) | The protocol, including what a server must do | Yes |
| [MCPB](https://github.com/modelcontextprotocol/mcpb) | Packaging a local server for one-click install | Optional, not done |
| [Server Cards, SEP-2127](https://modelcontextprotocol.io/seps/2127-mcp-server-cards.md) | Discovering remote HTTP servers | No: this is stdio |
| [AGENTS.md](https://agents.md) | Instructions for coding agents | Yes |
| [Agent Skills](https://agentskills.io/home) | Portable instructions | Yes: `skills/health-validation-mcp` |

**There is no standard for human-written MCP docs.** As is common practice, `make docs` generates
[tools.md](tools.md) and [tools.json](tools.json) from `tools/list`, and `TestToolDocsMatchTheServer` fails when
they go stale.

## Protocol

| Requirement | Level | Status | Tested |
|---|---|---|---|
| [`server/discover`](https://modelcontextprotocol.io/specification/2026-07-28/changelog.md) answers versions, capabilities, identity (`serverInfo` in `_meta`) | MUST | Yes, from the SDK | `TestItSpeaksTheLatestProtocol`, in `cmd/` and (e2e) |
| `resultType` on every result, `ttlMs` and `cacheScope` on `tools/list` | MUST | Yes, from the SDK | same |
| Older revisions, through `initialize` | MAY | Yes: 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05 are echoed back, with capabilities `{tools:{}}`. 2026-07-28 has no `initialize`, so asking for it, or for an unknown version, there gets 2025-11-25. Through `_meta`, an unknown version gets -32022 with the supported list (seen on the wire, not tested) | `TestOlderRevisionsThroughInitialize` |
| [stdout holds only MCP messages](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio.md), logs go to stderr | MUST | Yes | `TestItSpeaksTheLatestProtocol` fails on any stdout line that is not JSON, and finds the log on stderr |
| Exit when stdin closes | SHOULD | Yes, and a running evaluation is cancelled | `TestItSpeaksTheLatestProtocol` (e2e), `TestStdinCloseCancelsEvaluate` |
| [Stop work on `notifications/cancelled`](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/cancellation.md) | SHOULD | Yes: the handler follows the request's context, which the SDK cancels, and serve sees the request cancelled | `TestCancelNotificationReachesServe` (e2e) |
| Send no reply to a cancelled request | SHOULD on that page, MUST on the stdio page | Yes. go-sdk v1.8.0 sends one; `StdioTransport` drops the response to a request cancelled while it ran | `TestACancelledRequestGetsNoReply`, `TestCancelNotificationReachesServe` (e2e) |
| [Parse error](https://www.jsonrpc.org/specification#error_object) for a line that is not JSON, and the session goes on | JSON-RPC | Yes: -32700, and -32600 for JSON that is not a message. go-sdk v1.8.0 alone ends the session with exit 1. A bad batch or a line over 16 MiB still ends it | `TestABadLineGetsAnErrorAndTheSessionGoesOn`, `TestLineErrors` |
| `"arguments": null` does not crash the server | Robustness | Yes: read as `{}`. go-sdk v1.8.0 with jsonschema-go v0.4.3 panics on it for tools with defaults. go-sdk v1.9.0-pre.1, a pre-release, fixes it in `applySchema`; jsonschema-go v0.4.3 is still the newest. The guard stays until a stable go-sdk has the fix | `TestNullArgumentsDoNotEndTheServer`, `TestNullArgumentsKeepTheServerUp` (e2e) |
| Methods not in the capabilities are not found | Implied | Yes: `resources/*` and `prompts/*` answer -32601. A request before `initialize` with no `_meta` gets code 0 from go-sdk v1.8.0, which JSON-RPC does not define; pinned by the test, to report upstream | `TestUndeclaredMethodsAreNotFound` |
| [Deterministic tool order](https://modelcontextprotocol.io/specification/2026-07-28/server/tools.md) | SHOULD | Yes | `TestItSpeaksTheLatestProtocol` |
| Unknown tool is a protocol error, bad input is `isError` | Required | Yes | same |
| Structured results also come as text | SHOULD | Yes | same |
| Do not add deprecated [logging](https://modelcontextprotocol.io/specification/2026-07-28/deprecated.md) | SHOULD NOT | Yes: the SDK's default is overridden | `TestCapabilitiesAreOnlyTools` |
| Advertise `listChanged` only if the list changes | Implied | Not advertised: the list is fixed at start | same |

## Tools

| Requirement | Status | Tested |
|---|---|---|
| [Names](https://modelcontextprotocol.io/specification/2026-07-28/server/tools.md): at most 128 characters, safe characters, unique, with a service prefix (`hv_`) | Yes | `TestEvaluateIsOffByDefault` lists them |
| `title` and `description` on each | Yes | `TestEveryToolDeclaresAllFourHints` |
| `inputSchema` with `additionalProperties: false` | Yes | `TestBadArgumentsNeverReachTheService` |
| `outputSchema` on each | Yes, generated by the SDK | `docs/tools.json` |
| All four hints set in each tool's literal, plus `title` | Yes | `TestEveryToolDeclaresAllFourHints` |
| Read and write in separate tools, 15 tools or fewer | Yes: 5, all read-only | same |
| Every parameter described, with limits and `default` in the schema | Yes | `docs/tools.json`, checked by `TestToolDocsMatchTheServer` |
| Errors that reach the model name the failure and a next step, never serve's text or the address | Yes | `TestEveryHintNamesANextStep`, `TestErrorsHideTheirDetail` |
| Descriptions do not tell the model how to behave | Yes. The warning about node text is in the server's `instructions` | By reading |

## Security

| [Requirement](https://modelcontextprotocol.io/specification/2026-07-28/server/tools.md#security-considerations) | Level | Status | Tested |
|---|---|---|---|
| Validate all tool inputs | MUST | Yes: enums and bounded integers only | `TestBadArgumentsNeverReachTheService` |
| Access controls | MUST | Yes: evaluation off unless exactly `true`; loopback-only target; optional socket-owner check | `TestEvaluateNeedsExactlyTrue`, `TestConfigOnlyAcceptsLoopback`, `TestOwnerCheck` |
| Rate limit tool invocations | MUST | Yes: 10 per second, burst 20, for all tools, and one evaluation per 10 s by default. Malformed calls are refused before the limiter and never reach serve | `TestToolCallsAreRateLimited`, `TestEvaluateIsRateLimited` |
| Sanitize tool outputs | MUST | Yes: cleaned, capped, fenced, tenant text replaced | `TestOtherTextIsCleanedAndFenced`, `TestTenantTextIsWithheld` |
| [Use stdio for a local server](https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices.md) | SHOULD | Yes, and no port is opened | `TestNoListeningSocket` |
| OAuth, token audience, sessions | | Do not apply: no HTTP transport | |
| SSRF | | The one outbound target is fixed to loopback; no argument can change it | `TestConfigOnlyAcceptsLoopback`, `TestRedirectsAreRefused`, `TestProxySettingsAreIgnored` |

Details, and what was left out on purpose: [security.md](security.md).

## Packaging and repo

| Item | Status | Tested |
|---|---|---|
| `-h`, `-v`, `-d` and a one-line startup message on stderr | Yes. `-d` logs one line per call to serve, with no body | `TestHelpGoesToStdoutAndEnds`, `TestVersionGoesToStdoutAndEnds`, `TestDebugRunsTheServer`, `TestDebugLogsEachCallToServe`, `TestItSpeaksTheLatestProtocol` |
| `AGENTS.md`; `SECURITY.md` and `LICENSE` are the repository's | Yes | |

## Not done or not tested

- **e2e against a real `health-validation serve` on a GPU health-validation knows:** `make e2e-serve` ran
  against real serve v0.4.0-rc.1 on the build machine (an unknown architecture, `READY_LOW_COVERAGE`) and
  the Pi (discovery failure, `DRAIN`). No GPU check was proven, and the fixture-only cases (cancel, kill
  mid-call, second call, owner refusal) did not run against real serve.
- **e2e through the image:** `make e2e-image` passed once against a locally built image; its SIGTERM
  test skips, because the signal reaches the docker client, not the server.
- **[MCP Inspector](https://modelcontextprotocol.io/docs/2026-07-28/tools/inspector.md):** not run. To try it:
  `npx @modelcontextprotocol/inspector --cli health-validation-mcp --method tools/list`
- **[Conformance suite](https://github.com/modelcontextprotocol/conformance):** not run. Its server mode needs
  an HTTP URL, and this server has no listener by design.
- **Evaluations:** [docs/evals/evals.xml](evals/evals.xml) has 10 questions in the `mcp-builder` format.
  `make evals` checks each answer through the tools; no model has answered them yet.
- **Cisco `mcp-scanner`** (YARA and schema): 5 tools, all safe. Trivy, OSV and govulncheck: clean but for
  one cel-go advisory in code this server never calls. Run by hand with `make scan`, not in CI.
- **Other MCP clients:** only the Go SDK's client has been used.

## What to re-check

| Check | Where |
|---|---|
| Is there a newer revision? | [Latest spec](https://modelcontextprotocol.io/specification/latest), [changelog](https://modelcontextprotocol.io/specification/latest/changelog.md), [deprecated features](https://modelcontextprotocol.io/specification/latest/deprecated.md) |
| What is new in the docs? | [Docs index](https://modelcontextprotocol.io/llms.txt), [SEPs](https://modelcontextprotocol.io/seps) |
| Does the SDK support it? | [go-sdk releases](https://github.com/modelcontextprotocol/go-sdk/releases), then `go list -m -u github.com/modelcontextprotocol/go-sdk` |
| Has the tool guidance moved on? | [`tool-design.md`](https://github.com/anthropics/claude-plugins-official/blob/main/plugins/mcp-server-dev/skills/build-mcp-server/references/tool-design.md) |
| Has the catalog moved on? | `make docs`, then read the diff |
| Does it still hold on the wire? | `go test ./cmd/... -run TestItSpeaksTheLatestProtocol` |
