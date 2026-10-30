# Design proposals

Use a design proposal to agree on a substantial change before implementing it
and to preserve the reasons for the decision. Designs live with the code as
numbered Markdown documents. An accepted design records an agreed approach;
it does not mean the feature is implemented, released, or supported.

## When to write a design

Discuss the need for a design with a maintainer on the proposal issue. A design
is useful for a new collection backend or check domain, a public interface or
verdict change, a new integration, or a decision spanning several components.
It can also record a focused architectural decision without proposing a large
feature.

Routine fixes, documentation corrections, dependency updates, and checks that
follow an established design can use the normal contribution path. A design
document is not required for every PR.

## Propose and review

1. Start with the applicable
   [Governance](https://github.com/dsx-ai-factory/health-validation/blob/main/GOVERNANCE.md)
   path. Major architectural changes need an issue with the `rfc` label and
   `[RFC]` title prefix, open for at least 10 business days. Significant changes
   need a community issue or Discussion open for at least 5 business days and
   agreement from at least two maintainers before merging. Apply all relevant
   requirements; design discussion can proceed while these periods are open.
   For other feature proposals, open or reuse a feature issue describing the
   problem and scope. Follow
   [Contributing](https://github.com/dsx-ai-factory/health-validation/blob/main/CONTRIBUTING.md#proposing-features-or-checks)
   and obtain maintainer acknowledgment before implementing a feature.
2. Copy [the template](template.md) into this directory as
   `NNNN-short-description.md`. Choose the next unused four-digit number and
   check open design PRs for collisions. Keep the number and filename once
   merged; gaps are fine, and merged numbers are never reused.
3. Replace the guidance with the proposal. Keep it proportional to the change:
   use the relevant prompts, remove irrelevant ones, and state unresolved
   choices explicitly. Link the proposal issue and add an entry to the index
   below. The framework proposal itself is [0001](0001-design-proposals.md).
4. Open a design-only PR. Link it from the issue and ask maintainers to involve
   the affected component owners and consumers. Review the design in PR
   comments so discussion stays attached to the proposed text.
5. Resolve questions that block agreement. Record acceptance in the document
   and index when maintainers agree and the applicable governance requirements
   are met. Merge the accepted design before submitting its implementation PRs.
6. Link implementation PRs from the design and link the design from those PRs.
   Record what is implemented or deferred separately from design status. Update
   user documentation and validation evidence with the implementation.

[Governance](https://github.com/dsx-ai-factory/health-validation/blob/main/GOVERNANCE.md)
remains authoritative for approval counts and discussion periods, including
significant changes and major architectural RFCs. A design PR supplements that
process; it does not replace an RFC issue, shorten a discussion period, or grant
a release approval. Follow the existing signing and PR requirements.

The initial framework record [0001](0001-design-proposals.md) is the bootstrap
exception to the separate proposal-issue requirement:
[PR 252](https://github.com/dsx-ai-factory/health-validation/pull/252) contains
both its proposal discussion and design review. This exception applies only to
0001 and does not waive any applicable governance review requirements.

## Status and history

| Design status | Meaning |
| --- | --- |
| Proposed | Under discussion; the approach is not yet accepted. |
| Accepted | Maintainers have agreed to the approach under the applicable governance process. |
| Rejected | The proposal was declined; retain the reason and review link if the record is merged. |
| Superseded | A later accepted design replaces this decision; link to the replacement. |

A Proposed record may be merged to continue discussion when maintainers choose;
that merge alone does not imply acceptance or authorize its implementation.
Record the accepting PR or review when moving to Accepted. Keep the document
and index status consistent. Do not use Accepted to indicate implementation
progress: use implementation links and a short progress note instead.

Revise a proposal in its design PR. After acceptance, correct factual errors and
broken links, and add implementation references or clarifications through PRs.
For a materially different decision, open a new numbered proposal. Once the
replacement is accepted, mark the old record Superseded and link both ways.
Do not silently rewrite the original rationale to match later code.

## Writing for Health Validation

Keep the design close to the affected code. Distinguish catalog policy and
evaluation from raw collection in `collect/`, integration in `toolkit/`, and
agent scaffolding in `agentkit/`. A collection API and a verdict API are
different contracts. The catalog remains usable without hardware backends;
scheduling, stateful monitoring, and execution of remediation belong to
consumers. Explain relevant compatibility and validation implications rather
than restating the entire architecture in every proposal.

Design records are repository development material, not a supported-feature
catalog. They are linked here and from contributor documentation, separately
from the published product documentation. Keep them public-safe: use public
sources and sanitized examples; do not include private checks, internal URLs,
credentials, customer identifiers, or unpublished hardware details. For a
security report, follow
[Security](https://github.com/dsx-ai-factory/health-validation/blob/main/SECURITY.md)
instead of a public proposal.

## Index

| Number | Design | Status |
| --- | --- | --- |
| 0001 | [Review design proposals in the repository](0001-design-proposals.md) | Proposed |

## References

These examples informed the format; their project-specific governance does not
apply to Health Validation.

- [NVSentinel design template](https://github.com/NVIDIA/NVSentinel/blob/main/docs/designs/template.md)
  and [design index](https://github.com/NVIDIA/NVSentinel/tree/main/docs/designs).
- [NVSentinel design review example](https://github.com/NVIDIA/NVSentinel/pull/2026).
- [AICR design records](https://github.com/NVIDIA/aicr/tree/main/docs/design).
- [Kubernetes enhancement proposal template](https://github.com/kubernetes/enhancements/tree/master/keps/NNNN-kep-template).
