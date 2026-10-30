# 0001: Review design proposals in the repository

| Field | Value |
| --- | --- |
| Status | Proposed |
| Authors | Kushal Shankar |
| Created | 2026-10-29 |
| Issue | This documentation PR proposes the framework itself. |
| Design review | The PR introducing this record; acceptance is pending maintainer review. |
| Supersedes | None |

## Summary

Use one numbered Markdown record for a change that needs design agreement,
starting with a design-only PR and retaining the record as the rationale for
later implementation. Provide a [guide and index](index.md) and a
[copyable template](template.md) in `docs/design/`.

## Problem and scope

Health Validation has a catalog and evaluation engine, raw collection, toolkit
adapters and CLI, and agent scaffolding. A change can cross these boundaries or
affect the verdict consumed by another system. The reasons for a design need a
discoverable home beside the code, with review separate from implementation.

The existing contribution and governance documents already cover feature
discussion, review, and RFCs. This proposal adds a format and location for the
design, without changing those requirements. It does not select a backend,
change an API or verdict, require designs for routine fixes, or establish new
release or hardware qualification rules.

## Design and rationale

Keep proposals in one `docs/design/` directory, even when several components
are affected. Component names and links in each design make ownership visible
without splitting a cross-component decision among several logs.

Copy `template.md` to the next unused `NNNN-short-description.md`, link the
proposal issue, and add it to the index in the same PR. The template asks for
the problem, design, tradeoffs, compatibility, validation, and unresolved
decisions, with optional prompts for Health Validation concerns. Small decisions
can remain short.

Discuss and accept the approach under the existing governance process. Merge
the accepted design before submitting its implementation PRs. Track subsequent
implementation by links and evidence, separately from Proposed, Accepted,
Rejected, or Superseded design status. Preserve the rationale when a later
accepted proposal supersedes it.

## Alternatives and consequences

- A minimal decision log with only context and decision is easy to write but
  leaves interface compatibility, validation, and preimplementation review
  implicit. The template adds these prompts only where useful.
- A full enhancement system modeled on Kubernetes provides release tracking and
  readiness gates, but Health Validation already has governance and no need for
  SIG ownership, KEP metadata, or another approval mechanism here.
- Separate proposal and ADR directories would duplicate the same decision and
  require a handoff between documents. One record can carry the proposal through
  review and retain its accepted rationale.

The selected approach needs no new tooling. Authors and reviewers must keep
the index, status, and implementation links current. Explicit status prevents
a merged draft from being mistaken for an accepted or shipped feature.

## Compatibility and delivery

This is documentation-only. It changes no runtime behavior, Go dependencies,
wire contracts, CI, or governance. Add discovery links from the root README and
Contributing guide. Keep design history in the repository, separate from the
published product documentation and generated catalog/schema reference.

## Validation

Check that a contributor can find and copy the template, choose a number,
understand the review path, and link an implementation without assuming that
acceptance means delivery. Run the existing Markdown, path, and documentation
checks. No hardware validation is needed for this framework.

## Open questions and references

Maintainers need to accept the proposed convention before this record becomes
Accepted. Review requirements remain those in
[Governance](https://github.com/dsx-ai-factory/health-validation/blob/main/GOVERNANCE.md).
The guide's [references](index.md#references) provide the public examples behind
the format.

## Implementation

The introducing PR supplies the guide, template, this worked proposal, and
contributor links. No backend, feature API, or consumer integration is
implemented by this record.
