# Security

This server lets an LLM read NVIDIA health-validation's catalog and, when the operator allows it, ask the
node's `health-validation serve` for a verdict. It changes nothing, but one verdict starts real collection
on a GPU node (NVML, DCGM, the journal), and the text it returns comes partly from tenants. So the main
questions are how little a fooled model can do, and how much it can be fooled by what it reads.

A control is listed here only if a test enforces it. Tests named `Test...` are unit tests in `pkg/` and
`cmd/`, run over a real MCP session against a fake serve. Tests marked (e2e) are in `tests/e2e` and run
the built binary over stdio against `lab/hvfixture`, which is health-validation's real verdict server and
catalog over synthetic measurements. `make e2e-serve` runs the real-serve e2e tests against your own
`health-validation serve`; the fixture-only cases skip there, including cancellation, SIGTERM and the
owner refusal. See [Known limits](#known-limits).

## Threat model

| Asset | Worry |
|---|---|
| The GPU node | A model triggers collection storms: NVML opens, DCGM watches, a GPU reset blocked by an open handle |
| The scheduling decision | A barely-checked or forged PASS is read as healthy, and a job lands on a bad node |
| Other local services | The server is turned into a client of something other than serve |
| The LLM's context | Tenant text in a verdict (process command lines, journal lines) carries instructions, and the LLM follows them |
| Tenant data | Command-line arguments, secrets included, leak into the model's context |
| The public image, and anyone who pulls it | A tampered dependency, base image or build |

| Attacker | Path |
|---|---|
| A prompt-injected LLM | Hostile tool arguments, or tools called in a hostile order or very often |
| A tenant on the node | Process names and command lines that end up in `gpu_compute_apps` and `gpu_xid_*` messages |
| A local process | Listening on serve's port while serve is down, and answering with a forged verdict |
| A supply-chain attacker | Dependencies, bases, the registry |

There is no network attacker: the server speaks stdio and opens no port (`TestNoListeningSocket`, which
first proves its detector sees a real listener). Its one outbound target is serve on loopback.

## Controls

### Reaching the wrong target

| Control | Proved by |
|---|---|
| Serve's host is exactly `127.0.0.1` or `::1`, the port 1024 to 65535; anything else stops the server at start | `TestConfigOnlyAcceptsLoopback`, `TestABadSettingStopsTheServer` |
| No tool argument names a host, URL, node or context; an unknown field is refused | `TestBadArgumentsNeverReachTheService` |
| The request body is fixed: only `cadence` and `xidFrom`, never `nodeId`, `executionContext` or `xidTo` | `TestRequestBodyIsFixed` |
| `HTTP_PROXY` and friends are ignored: the transport has no proxy function at all | `TestProxySettingsAreIgnored` |
| Redirects are refused, not followed | `TestRedirectsAreRefused` |
| Answers over 4 MiB are refused | `TestBodiesAreCapped` |
| With `HV_MCP_SERVE_UID` set, the listening socket's owner is read from `/proc/net/tcp` (or `tcp6`) before each call, and another owner is refused | `TestListenerOwner`, `TestOwnerCheck`, `TestOwnerCheck` (e2e) |

The owner check is optional and Linux only. Without it, a local process that takes serve's port while
serve is down can answer. It can still only return something that decodes as a schema `0.7` verdict (see
below), and the README says to set it.

### Collection on the node

| Control | Proved by |
|---|---|
| `hv_get_verdict` is not registered unless `HV_MCP_ALLOW_EVALUATE` is exactly `true`; `1`, `TRUE`, `True`, `yes` and padded values leave it off and are logged | `TestEvaluateIsOffByDefault`, `TestEvaluateNeedsExactlyTrue` |
| One evaluation at a time: a second is refused, not queued, and serve sees one request | `TestSingleFlightRefusesNotQueues` |
| At most one evaluation per `HV_MCP_EVALUATE_INTERVAL_SECONDS` (default 10, 5 to 600) | `TestEvaluateIsRateLimited`, `TestEvaluateIntervalBounds` |
| All tool calls share a limit of 10 per second, burst 20, as the spec requires. It counts calls that pass schema validation; malformed calls are refused earlier and never reach serve | `TestToolCallsAreRateLimited`, which checks the numbers 10 and 20 themselves |
| Nothing is retried, an HTTP 429 included | `TestHTTPStatusMapsToKinds` |
| `hv_service_status` reads `/healthz` only and starts no evaluation. It reads whether an evaluation runs from a flag, never from the evaluation lock, so it cannot make a concurrent `hv_get_verdict` look busy | `TestServiceStatus`, `TestStatusLeavesTheEvaluationLockAlone` |
| SIGTERM, closing stdin, `notifications/cancelled`, or the server's shutdown cancels the in-flight request, and serve sees it cancelled. A request cancelled by `notifications/cancelled` gets no reply | `TestACancelledRequestGetsNoReply`, `TestSigtermCancelsEvaluate`, `TestStdinCloseCancelsEvaluate`, `TestShutdownCancelsEvaluate`, `TestCancelNotificationReachesServe` (e2e), `TestSigtermCancelsEvaluate` (e2e) |

The real limits on load are serve's: its serialized collection, per-source watchdogs, 32-request admission
and `collection_source_busy`. This server adds its own limits so a model cannot reach those.

### Hostile arguments

- Every argument is an enum or a bounded integer, and unknown fields are refused, before any tool code
  runs. `TestBadArgumentsNeverReachTheService` sends 19 hostile calls (a URL, shell text, a path in
  `check_id`, out-of-range pages and lookbacks, `node_id`, `execution_context`, `xid_to`) through a real MCP
  session; each is refused, the fake serve records no request, and no audit line is written.
- The `check_id` enum and the other catalog enums are generated from the compiled-in catalog, so they cannot
  offer a value the catalog lacks (`TestGeneratedEnumsMatchTheCatalog`).
- `execution_context` is not offered at all. `OUT_OF_BAND` is declared by no registered check, so it would
  only produce an all-skipped PASS.

### Hostile output

| Control | Proved by |
|---|---|
| Control and format characters (ANSI escapes, bidi overrides, zero-width) are stripped | `TestCleanStripsControlAndFormatCharacters`, `TestOtherTextIsCleanedAndFenced`, `TestServiceStatus` |
| Node text is wrapped in a marker with a random value per call, and cannot close it | `TestFenceCannotBeClosedFromInside`, `TestOtherTextIsCleanedAndFenced` |
| Messages are capped at 300 bytes, stuck calls at 8, entities at 16, each cut flagged. The tests check these numbers, not only the constants | `TestOtherTextIsCleanedAndFenced` (a 1,000-byte message), `TestDocumentedCaps`, `TestServiceStatus`, `TestEntitiesKeepOnlyIdentifiers` |
| `hv_describe_check` is capped at 16 KiB, and remediation lists at 30 checks, with the cut flagged | `TestDescribeCheck`, `TestExplainRemediation`, each checking the number |
| `gpu_compute_apps` messages, which hold tenant command lines, become a count and the PIDs. A PID is read only from the `pid=N` at the start of a line, never from a `pid=` inside the quoted command line, and no more PIDs are shown than the reported count | `TestTenantTextIsWithheld`, `TestPIDsComeFromPidFieldsOnly`, `TestPIDsNeverExceedTheCount`, `TestSentencesHoldNoTenantText` |
| An `UNKNOWN` `gpu_compute_apps`, whose collection failed, says the process list could not be read. It never says processes hold the GPU | `TestUnreadComputeAppsHoldNoProcess` |
| An `UNKNOWN` `gpu_xid_*`, as when the journal cannot be read, says no Xid was confirmed, never that one fired. All of them fold into one finding with `folded_checks`, so they take one of the 50 finding slots | `TestUnreadJournalIsNotAnXid` |
| `gpu_xid_*` messages and observed values, which hold journal text and tenant process names, become the compiled-in catalog's own text | `TestTenantTextIsWithheld`, `TestSentencesHoldNoTenantText` |
| Entity names are kept only if they look like a GPU UUID, a PCI address, an RDMA port (`mlx`, `ib`, `bnxt_re` or `rxe` device names, then `:port`) or a GPU index; others are counted. Kept names are pattern-checked, not fenced | `TestEntitiesKeepOnlyIdentifiers`, `TestOtherTextIsCleanedAndFenced`, `TestUnknownIDsAreFenced` |
| Check IDs pass as is only when the compiled-in catalog or serve itself names them, or they have the `gpu_xid_<code>` shape. Any other ID that fits `[a-z0-9_]{1,64}` is fenced, and anything else is replaced. Observed-value keys are fenced unless a catalog check reads a signal of that name | `TestUnknownIDsAreFenced` |
| `node_id` is never returned | `TestNodeIDIsDropped` |
| A failed call to serve is reported by kind only; serve's text, the address and the error detail go to stderr | `TestErrorsHideTheirDetail`, `TestServiceNotRunning`, `TestHTTPStatusMapsToKinds` |

Fencing helps the model but cannot guarantee it ignores what it reads. The real limit is what the tools
can do: a fooled model still has five read-only tools.

### A verdict that is wrong or barely checked

| Control | Proved by |
|---|---|
| A verdict that is not JSON, lacks a severity, has an unknown or unspecified enum name or number anywhere (check results, skipped checks, the domain map and recommended actions included), or a schema other than `0.7`, is refused; the advice is `NO_VERDICT`, never a pass | `TestDecodeVerdict`, `TestABadVerdictIsNeverAPass` |
| The verdict must also pass health-validation's own wire checks (`verdictproto.FromProto`: every check names its category, severity and the other required fields), every node or check action names its state, and the overall severity is not lower than any check's | `TestDecodeVerdict`, `TestABadVerdictIsNeverAPass` |
| Object keys the 0.7 schema does not define are dropped at any depth before the strict decode, so a later serve may add fields. Duplicate keys and data after the verdict are refused | `TestDecodeVerdict`, `TestABadVerdictIsNeverAPass` |
| A PASS where no GPU or NVLink check ran is `READY_LOW_COVERAGE`, with the reason, however many Xid checks ran: they only read the journal. This covers an H200 at `CONTINUOUS` and a serve that supplied no GPU measurements | `TestUnknownArchitectureIsNotReadAsHealthy`, `TestXidChecksAloneAreLowCoverage`, `TestSchedulingAdvice` |
| An UNKNOWN whose first step is `retry_collection` (a busy source, a collection timeout) is `RETRY_COLLECTION`; CRITICAL and any other UNKNOWN (`check_collection` included) are `DRAIN`; WARNING is `MONITOR`; INFO is `READY` | `TestSchedulingAdvice` |
| A serve on another catalog version is flagged `version_skew` | `TestVersionSkewIsFlagged` |
| Serve down, timing out or failing gives `NO_VERDICT` | `TestServiceNotRunning`, `TestHTTPStatusMapsToKinds`, `TestShutdownCancelsEvaluate` |

### Audit

Every evaluate attempt writes one JSON line to stderr with the tool, its arguments, the outcome and the
duration, refusals included. A call refused by the global call limit gets the same fields as any other.
The outcomes are `ok`, `refused`, `refused_busy`, `refused_rate`, `not_running`, `busy`, `timeout`,
`cancelled`, `unsupported` (a schema other than `0.7`), `wrong_owner` (the owner check refused the port)
and `no_verdict` (`TestAuditLineHasArgumentsOutcomeAndDuration`, `TestSingleFlightRefusesNotQueues`,
`TestEvaluateIsRateLimited`, `TestToolCallsAreRateLimited`, `TestServiceNotRunning`,
`TestHTTPStatusMapsToKinds`, `TestShutdownCancelsEvaluate`, `TestItSpeaksTheLatestProtocol`).

With `-d`, each call to serve also writes one debug line: method, path, HTTP status, duration and error
kind, never a body or serve's text (`TestDebugLogsEachCallToServe`; `TestDebugLinesHoldNoBody` runs against
a serve that puts a marker in every body and checks that no stderr line holds it and that debug lines have
only those keys).

### Protocol surface

The server advertises only `tools`: no `logging`, no `listChanged` (`TestCapabilitiesAreOnlyTools`,
`TestItSpeaksTheLatestProtocol`). `resources/*` and `prompts/*` answer -32601, method not found, as the
capabilities say; the SDK alone would answer them with empty lists (`TestUndeclaredMethodsAreNotFound`).

Hostile input on the wire does not stop the server:

- `"arguments": null` is read as `{}`. go-sdk v1.8.0 and jsonschema-go v0.4.3 panicked on it for the three
  tools with defaults, which ended the process and dropped a running evaluation without its audit line
  (`TestNullArgumentsDoNotEndTheServer`, `TestNullArgumentsKeepTheServerUp` (e2e)).
- A panic anywhere in `tools/call`, the SDK's own code included, returns `error executing the tool`
  (`TestAPanicInAToolCallIsAToolError`).
- A line that is not JSON gets -32700, and JSON that is not a JSON-RPC message gets -32600; the session
  goes on (`TestABadLineGetsAnErrorAndTheSessionGoesOn`, `TestLineErrors`). The SDK alone ended the session.
  A bad JSON-RPC batch and a line over 16 MiB still end it, as the SDK does. Every tool is marked read-only, not destructive and closed-world
(`TestEveryToolDeclaresAllFourHints`).

### The image

These are properties of the [Dockerfile](../Dockerfile). `make e2e-image` runs the fixture e2e tests with
the server in a locally built image, with the flags below; no test checks the image's own contents.

- One static binary (`CGO_ENABLED=0`) on `distroless/static-debian13:nonroot`: no shell, package manager or
  libc. User 65532.
- Bases pinned by digest, Go dependencies by `go.sum`, with `GOTOOLCHAIN=local` so a `go.mod` change cannot
  download a toolchain. `Dockerfile.dockerignore` lets in only the Go sources the build needs.
- It needs no devices, mounts, journal or GPU access.

Run it with everything off. It must share serve's network namespace to reach loopback:

```shell
docker run --rm -i --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --memory=128m --cpus=1 --pids-limit=64 --network=host \
  health-validation-mcp:local
```

Use
`--network=container:<serve container>` when serve runs in a container without host networking. Add
`-e HV_MCP_ALLOW_EVALUATE=true` only for a client that should evaluate the node. The image is not published.
Scan it with `make scan`.

## Left out on purpose

- **Any tool that acts on the node:** health-validation only recommends. Remediation belongs to the
  scheduler or a remediation system.
- **Running the hv binary, or linking NVML or DCGM:** that would put GPU and journal access in the process
  that parses model input, and `run` has no signal handler, so killing it can leave DCGM state behind.
- **Auth or TLS on the transport:** stdio has no listener.
- **Auth to serve:** serve has none to offer. The optional owner check is the substitute.
- **A verdict cache:** health-validation does not cache verdicts, and a cached one would be stale.
- **Asking for human confirmation in the server:** an agent client may answer it itself. The gate is the
  operator's `HV_MCP_ALLOW_EVALUATE`.

## Known limits

- **No GPU check proven against a real serve.** The unit tests use a fake serve built from
  health-validation's own engine and wire code. The e2e tests run the built binary against
  `lab/hvfixture`, health-validation's real verdict server over synthetic measurements; their results are
  in [test-results/fixture](test-results/fixture). `make e2e-serve` checks the wire and
  the decode against a real `health-validation serve`. Under `make e2e-image` the SIGTERM test skips,
  because the signal reaches the docker client, not the server.
- A panic in a tool returns only an error, with detail in the log. The guard in `register` names the tool
  and no test triggers it; the guard around all of `tools/call` is tested.
- The 64 KiB cap on `hv_get_verdict` output is in the code but no test reaches it.
- The owner check is Linux only, and off unless `HV_MCP_SERVE_UID` is set.
- Tool annotations are hints: only a client that honours them prompts a human.
- Fencing lowers the risk from prompt injection but does not remove it.
