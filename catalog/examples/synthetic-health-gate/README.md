# Synthetic health gate

A hardware-independent, typed in-process caller of the catalog. Synthetic
measurements pass through the real evaluator. The caller writes the
`health.NodeVerdict` JSON, then continues a harmless synthetic workload only when
both mandatory checks were assessed PASS.

The fixed catalog uses `NewEmpty`, the unmodified `cpu.CPUCount()`, and an
example-local `synthetic_assessment` check. The latter is explicitly Private and
Experimental, not NVIDIA-endorsed. Its `assessment_available == false` rule
produces UNKNOWN through normal evaluation. A missing measurement is a skip;
a collection failure is CRITICAL. Neither is relabeled UNKNOWN.

`synthetic` is an architecture label required by the catalog API, not hardware
detection. No GPU, sensor, device access or hardware collection is involved.
CPU values of 8 possible and 8 online are fixture data. CPUCount tests inequality,
not the trustworthiness of arbitrary counts, including equal zero counts.

## Run locally

From the repository root, with Go 1.27.3, for local iteration:

```sh
go build -o /tmp/synthetic-health-gate ./catalog/examples/synthetic-health-gate
/tmp/synthetic-health-gate -scenario pass
```

An explicit `-scenario` is required. Choose one of these seven cases:

| Scenario | Actual evaluated outcome | Exit | Continuations |
|---|---|---:|---:|
| `pass` | CPU 8/8 PASS; assessment true PASS | 0 | 1 |
| `critical` | CPU 8/7 CRITICAL; assessment PASS | 1 | 0 |
| `unknown` | CPU PASS; assessment false UNKNOWN | 1 | 0 |
| `missing` | Overall PASS; CPU SENSOR_MISSING; assessment PASS | 1 | 0 |
| `disabled` | Overall PASS; CPU CHECK_DISABLED; assessment PASS | 1 | 0 |
| `collection-error` | CPU CRITICAL with a collection recommendation; assessment PASS | 1 | 0 |
| `empty` | Overall PASS; no results; both checks SENSOR_MISSING | 1 | 0 |

```sh
/tmp/synthetic-health-gate -scenario critical
/tmp/synthetic-health-gate -scenario unknown
/tmp/synthetic-health-gate -scenario missing
/tmp/synthetic-health-gate -scenario disabled
/tmp/synthetic-health-gate -scenario collection-error
/tmp/synthetic-health-gate -scenario empty
```

These are the executable's normal-return exit codes. On Unix, a broken stdout or
stderr pipe may terminate it with [SIGPIPE](https://pkg.go.dev/os/signal#hdr-SIGPIPE)
first. A `go run` or build-system wrapper may report a different process status.
JSON goes to stdout. Withhold reasons,
errors and `continued synthetic workload` go to stderr. Recommendations remain
data; the example executes no remediation.

## Caller contract

The order is evaluate, decide, write JSON, branch on the decision, then optionally
continue. The requirement is always exactly `cpu_count` in category CPU and
`synthetic_assessment` in category Node, each once. An empty or narrowed registry
cannot shrink this requirement.

| Condition, in order | Decision | Exit |
|---|---|---:|
| Invalid scenario, construction or evaluation error | Surface error | 2 |
| Schema missing or not literal `0.4`, empty catalog version, unknown numeric severity | Reject typed contract | 2 |
| Any skip, missing/duplicate/unexpected ID, wrong category, skipped result, or no results | Withhold coverage | 1 |
| Complete coverage; worst overall/per-check severity INFO, WARNING, CRITICAL or UNKNOWN | Withhold that severity | 1 |
| Supported contract; complete coverage; overall and both checks PASS | Write JSON, continue once | 0 |
| Returned encoding/output or continuation error | Surface error without retry | 2 |

A returned verdict-output error prevents continuation but may leave partial stdout. A
continuation failure occurs after valid JSON. Evaluation and typed-contract
errors produce no stdout. A misleading overall PASS cannot override a non-PASS
check. UNKNOWN orders above CRITICAL.

This PASS-only policy belongs to this caller. The stock operator CLI deliberately
uses exit 0 for PASS/INFO/WARNING, 1 for CRITICAL/UNKNOWN, and 2 for runtime errors.
Skips block this example because both checks are mandatory here; skips are not
universally unhealthy. This point-in-time synthetic verdict establishes neither
real node readiness nor stock-catalog coverage, production safety or performance.

The JSON keeps the library's versions, timestamps, trace/span IDs, domain
summaries and recommendations. Only optional `node_id` is set to `synthetic-node`.
Empty `check_results` remains null; empty `skipped_checks` is omitted.
`catalog_version` identifies the library build, not this custom check or the
source commit. Schema support is intentionally pinned to literal `0.4`; future
versions require review. There is no external JSON input or full JSON Schema
instance validator.

## Tests and build integration

```sh
go test -count=1 -v ./catalog/examples/synthetic-health-gate
go test -count=1 ./catalog/...
```

Tests exercise all seven real evaluator cases, signal uniqueness and coverage,
the actual continuation branch and output-before-continuation ordering. A
test-only severity-only reference accepts the same real missing-CPU verdict that
the caller withholds. Separate typed boundary fixtures cover INFO/WARNING,
schema/identity corruption and misleading summaries. Error injections cover
evaluation, output and continuation failures. Actual emitted bytes are checked
for required keys, string enums, typed fields and exactly one document with EOF;
this is contract testing, not full schema validation.

Bazel is the repository's canonical build and test system. From the repository
root:

```sh
bazel build //catalog/examples/synthetic-health-gate
bazel test --config=ci //catalog/examples/synthetic-health-gate:synthetic-health-gate_test
bazel run //catalog/examples/synthetic-health-gate -- -scenario pass
```

Regenerate `BUILD.bazel` with `bazel run //tools/gazelle` after changing Go files
or imports. Build-system wrapper statuses can differ from the executable's own
status, as described above.
