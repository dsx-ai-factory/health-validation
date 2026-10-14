# Support for NVIDIA Health Validation

This document describes how to get help with NVIDIA Health Validation and what
to expect from upstream support for the check catalog, toolkit, CLI, and
container image. Support under an NVIDIA commercial agreement is governed by
that agreement.

## Support channels

| Channel | Best for | Who | Response expectation |
| --- | --- | --- | --- |
| [Bug report](https://github.com/dsx-ai-factory/health-validation/issues/new?template=bug.yml) | Incorrect behavior, wrong verdicts, or crashes | Anyone | Maintainers aim to triage within 3–5 business days |
| [Task / Question](https://github.com/dsx-ai-factory/health-validation/issues/new?template=task.yml) | Usage questions or documentation gaps | Anyone | Best effort; maintainers aim to respond within 3 business days if unanswered |
| [Feature request](https://github.com/dsx-ai-factory/health-validation/issues/new?template=feature.yml) | New checks, signals, threshold changes, or other improvements | Anyone | Best effort; maintainers aim to respond within 3 business days if unanswered |
| Private reporting through [SECURITY.md](./SECURITY.md) | Potential security vulnerabilities | Anyone | Follow the NVIDIA PSIRT process |
| NVIDIA support under your commercial agreement or through your NVIDIA account team | Production incidents and contract-governed requests | Customers with an applicable support agreement | Defined in the agreement |

GitHub Discussions is currently disabled. Use the Task / Question form for
questions and the Feature Request form for proposals. Filing a proposal does
not commit the project to implementing it; follow the proposal process in
[CONTRIBUTING.md](./CONTRIBUTING.md). Public channels are not appropriate for
production incidents or confidential information.

## Support requests and bug reports

A support request asks how to use Health Validation. A bug report describes
incorrect or unexpected behavior in the project. A health check detecting an
unhealthy node is not, by itself, a software defect; explain why you believe
the result is incorrect.

Before opening a request, search
[existing issues](https://github.com/dsx-ai-factory/health-validation/issues)
and check the [README](./README.md), [FAQ](./docs/faq.md), or
[integration guide](./docs/integration.md) for the relevant guidance.

### What to put in a bug report

Bug reports must include:

- **Version:** the affected component and installation method. For the CLI,
  include `health-validation version --output json`; for embedded use, list
  the module versions involved. For source builds or containers, include the
  commit SHA or image tag and digest, and describe any local changes.
- **Reproduction:** the command or Go API call, steps, relevant inputs and
  configuration, and expected and actual results. Provide a minimal
  reproduction where possible.
- **Output:** complete, sanitized verdict JSON or the error message. For CLI
  reports, also include stderr diagnostics and the process exit code. Preserve
  `schema_version` and `catalog_version` when present. If no output was
  produced, say so.
- **Environment:** relevant OS, kernel, CPU architecture, GPU model, driver,
  and dependency versions. For containers, include relevant runtime, mount,
  and permission details.

Check whether the defect reproduces on current upstream `main` when feasible,
and include the tested SHA. If you cannot test `main`, explain the limitation.
Reports against Current or Maintenance releases are accepted when `main` is
already fixed or hardware access prevents testing it. Identify any known fix
and the last working version, if known, so maintainers can assess a backport.
Testing `main` is not a prerequisite for filing a report.

For an incorrect verdict or skipped check, include the `check_id`, observed
values, thresholds and overrides, disabled checks, and the expected result.
Include relevant execution context, cadence, topology, skip reasons, and
sanitized journal events. A small Go reproduction using supplied measurements
can help isolate a catalog defect without access to the original hardware.

For collection failures, identify the sensor or backend, error or timeout,
and relevant access to NVML, sysfs, or the host journal. For integration or
metrics problems, include the sanitized invocation or manifest, component
versions, configuration, and diagnostics relevant to the failure.

Remove credentials, tokens, authentication headers, customer data, and private
host or cluster identifiers before sharing output. Verdicts and journal logs
may also contain device identifiers and application command lines. Use
consistent replacements where relationships matter to the reproduction.

### Configuration and custom extensions

Documented threshold overrides and check disabling are configuration features.
Include them in your report; fixed thresholds cannot be overridden.

Authors are responsible for their private checks, custom collection code, and
fork changes. If an extension exposes a suspected upstream defect, reproduce
it with built-in checks and stock collection where possible, or provide a
minimal catalog reproduction with supplied measurements. See the
[extension guide](./docs/extending.md).

## Upstream community support

NVIDIA Health Validation is a **Maintained** project. Its maintainers and
community provide best-effort upstream support. These are response targets,
not promises to fix a reported problem:

- Maintainers aim to triage new bug issues within **3–5 business days**. Triage
  may mean acknowledging the report, requesting details, or confirming a defect.
- Pull requests aim to receive an initial response within **5–7 business days**.
  An initial response is not necessarily a full review.
- Maintainers aim to respond to unanswered questions and feature proposals
  within **3 business days**. There is no hard community-support SLA.
- Security reports receive priority through the private process in
  [SECURITY.md](./SECURITY.md). Response and disclosure follow the
  [NVIDIA PSIRT policies](https://www.nvidia.com/en-us/product-security/psirt-policies/).
  Do not disclose vulnerabilities in public issues or pull requests.

Resolution depends on severity, reproducibility, and maintainer capacity.
Upstream maintainers do not provide production incident support or a guaranteed
fix date. Report defects in third-party components to their owners. For urgent
production incidents, use your operational support channel or an applicable
NVIDIA support agreement.

Follow the [Code of Conduct](./CODE_OF_CONDUCT.md) when participating. See
[CONTRIBUTING.md](./CONTRIBUTING.md) for proposals and code contributions.

## Support lifecycle

The `catalog` and `toolkit` modules are versioned independently using
[Semantic Versioning](https://semver.org/). Their tags use `catalog/vX.Y.Z` and
`toolkit/vX.Y.Z`; their versions need not match. CLI and container release
support follows the toolkit version and includes its bundled catalog version,
not arbitrary combinations of module versions.

This lifecycle takes effect on adoption of this policy. It covers final
catalog and toolkit releases available at adoption and subsequent final
releases, including pre-1.0 releases. The experimental `collect` and `agentkit`
modules are outside this lifecycle.

| Stage | Meaning |
| --- | --- |
| Current | Receives features, bug fixes, and security fixes. |
| Maintenance | Eligible for security and critical bug fixes only; no new features. |
| End of life (EOL) | Receives no upstream fixes. Upgrade to a supported release. |

Within a module's current major version, the latest final minor is Current and
the previous released final minor is in Maintenance. Older minors are EOL,
subject to the catalog dependency exception below. A new final minor moves the
former Current minor to Maintenance and the former Maintenance minor to EOL.
Patch releases and release candidates do not advance these stages. There is
no fixed release cadence.

When a module's first final release of a new major ships, the previous major's
latest released final minor remains in Maintenance for three months, then
reaches EOL. Other minors of that major are EOL, subject to the catalog
dependency exception below. This also applies to the `v0`→`v1` transition.
Breaking pre-1.0 minor releases follow the minor lifecycle above rather than
receiving a separate three-month tail.

A catalog minor used by a supported final toolkit release remains eligible
for security and critical fixes needed by that toolkit release, even after
the catalog's own support window ends. The exception ends when no supported
toolkit release depends on that catalog minor. It can extend the two-minor
window or previous-major tail; it does not provide feature support.

### Supported release lines

| Module | Release line | Stage |
| --- | --- | --- |
| catalog | `v0.3.x` | Current |
| catalog | `v0.2.x` | Maintenance |
| toolkit | `v0.3.x` | Current |
| toolkit | `v0.2.x` | Maintenance |

The `v0.1.x` lines of both modules are EOL.

Maintainers update this table and the release notes when final releases change
the supported lines. Backport requests should identify the affected supported
line. Maintainers evaluate security and critical fixes for Maintenance lines
on a best-effort basis; see [RELEASE.md](./RELEASE.md).

### Interface compatibility

Both modules are currently **pre-1.0 (`v0.x`)**. Breaking public API or CLI
changes may occur in minor releases; compatible fixes use patch releases.
From 1.0, breaking changes to the catalog's public Go API or the CLI's
behavior and flags require a major release. Breaking changes are announced
in advance. A release deprecating a public API must document a migration path
and retain the API until a later breaking release removes it.

Pin a release version or container digest for reproducible deployments.
Release candidates, `main`, and the `latest` image contain prerelease or
development changes; `latest` follows `main`. Review the
[changelog](./CHANGELOG.md),
[release notes](https://github.com/dsx-ai-factory/health-validation/releases),
and [release policy](./RELEASE.md) before upgrading. Development builds and
release candidates are outside the finalized-release lifecycle.

The catalog is the intended integration and extension surface, subject to the
pre-1.0 policy above. Toolkit sensors, backends, collection wiring, and the app
builder are reference implementations and may change. The `collect` and
`agentkit` modules are experimental. See the
[developer guide](./docs/developer.md) and [extension guide](./docs/extending.md).

Verdict JSON is versioned separately through `schema_version`.
`catalog_version` identifies the catalog compiled into the producer. Validate
output against the [JSON Schema](./catalog/health/node-verdict.schema.json) at
the producer's tag or commit before acting on it. Treat missing or unrecognized
schema versions as untrusted output.

### Supported environments

Stock hardware collection through `health-validation run` requires Linux
x86_64 or aarch64 with the NVIDIA driver and NVML. The app constructs
NVML-backed GPU, node, and NVLink sensors, plus CPU, Xid, and RDMA link-flap
sensors.

For integrations using `toolkit.Collect`, prerequisites depend on the supplied
sensors and backends. With the stock toolkit backends, CPU collection requires
Linux sysfs; Xid and RDMA link-flap collection require host journal access
through `journalctl`. These backend requirements also apply to the stock app.
The catalog can evaluate supplied measurements on other platforms without
hardware access.
See the [prerequisites](./README.md#prerequisites) and
[integration guide](./docs/integration.md).

The catalog focuses on GB200 and GB300. GB200 coverage is validated; GB300
remains experimental pending equivalent validation. Coverage depends on each
check's architecture, execution context, required signals, and cadence. See
the [check catalog](./README.md#check-catalog).

A verdict describes one node at the time of evaluation. Inspect per-check
results and `skipped_checks`; PASS or exit code `0` alone does not establish
full coverage. Collection failures can produce unhealthy results without
establishing a hardware fault.

### Upgrades and downgrades

| From → to | Support status |
| --- | --- |
| Older minor → latest Current minor in the same major | Supported; pre-1.0 minor releases can break compatibility. Follow migration guidance for intervening releases. |
| Older patch → newer patch in the same minor, skipping intermediate patches | Supported; patches provide compatible fixes. |
| Previous major's latest minor → new major's first release | Supported; follow its migration guidance. |
| Skipping a major entirely | Outside upstream support; upgrade through the intermediate major. |
| Downgrading to an older release | Outside upstream support. |

These are upstream support paths, not a matrix of validated hardware or
deployment upgrades. Preserve the supported toolkit release's catalog version
and validate verdict consumers against the producer's schema.

## Enterprise and production support

For enterprise support options, contact your NVIDIA account team or visit
[NVIDIA Support](https://www.nvidia.com/en-us/support/). If you have a commercial
support agreement, use its designated route for covered production incidents.
The agreement determines what it covers and its response times. You may also
report an upstream software defect through GitHub after removing confidential
information.

Health Validation reports verdicts and recommended actions. Your scheduler,
controller, or remediation system performs the actions. Follow the
[remediation guide](./docs/remediation.md) and re-evaluate the suite between
actions. For `service_required`, use your CSP's service path for a CSP-managed
cluster or your organization's service process on premises.

## Escalation paths

| Situation | Next step |
| --- | --- |
| An issue has not received a response within the target | Follow up in the existing issue with the current impact and request maintainer review. [MAINTAINERS.md](./MAINTAINERS.md) identifies the team. |
| A question reveals a software defect | Open a bug report and link the question and reproduction. |
| A new check or behavior change is needed | Open a feature request with the failure mode and proposed behavior; follow [CONTRIBUTING.md](./CONTRIBUTING.md) before implementation. |
| A result recommends `retry_collection` or `check_collection` | Check the timeout, driver, permissions, and sensor access before assuming a hardware fault. |
| Remediation is exhausted or the result recommends `service_required` | Use your cluster's service path with the verdicts and attempted actions. |
| A defect affects a Current or Maintenance release | Provide the affected version, reproduction, and any known fix on `main`; identify the target line for a backport request. |
| A defect affects an EOL release | Upgrade to a supported release or use contract-governed support if available. |
| A report may involve a security vulnerability | Follow [SECURITY.md](./SECURITY.md) privately. Stop discussing details publicly and ask a repository administrator to remove any public disclosure. |
