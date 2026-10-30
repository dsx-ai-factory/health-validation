# NNNN: Design title

Copy this file following the [design guide](index.md). Replace the guidance with
the proposal, remove prompts that do not apply, and keep small decisions short.

| Field | Value |
| --- | --- |
| Status | Proposed |
| Authors | Names or GitHub handles |
| Created | YYYY-MM-DD |
| Issue | Public proposal issue link |
| Design review | Design PR link; add when opened |
| Supersedes | None, or links to earlier designs |

## Summary

Describe the proposed change and the outcome for a user or integrator in one
short paragraph. Identify the decision reviewers need to make.

## Problem and scope

Explain current behavior, the problem, goals, and non-goals. Link the relevant
code or public evidence. Identify affected components and consumers; distinguish
existing behavior from proposed behavior.

## Design and rationale

Describe the behavior, interfaces, data flow, and why the approach fits the
problem. Include a small example or diagram when it clarifies the contract.
Address the applicable questions below rather than treating them as a checklist
for every design:

- Which layer owns the change: catalog types/rules, raw collection, toolkit
  adapters or CLI, agentkit, or a downstream consumer? Preserve dependency
  boundaries and distinguish raw observations from evaluated verdicts.
- For checks and sensors, what are the signals, units, entity identities,
  collection windows, architecture/context/cadence applicability, and threshold
  governance? How do missing or unsupported data, collection errors, skips,
  UNKNOWN results, and observed faults differ?
- For interfaces and integrations, show inputs, outputs, failure behavior, and
  relevant timeouts or resource ownership. Who schedules calls, retains cursors
  or history, recognizes recovery, and executes recommended actions?
- For hardware backends or deployment changes, explain runtime dependencies,
  permissions and access, supported environments, and behavior when dependencies
  are absent. Use only public information and sanitized examples.

## Alternatives and consequences

Compare realistic alternatives, including keeping current behavior. Explain the
choice, benefits, costs, failure risks, and mitigations. State any tradeoff the
proposal deliberately leaves with consumers.

## Compatibility and delivery

Describe effects on existing consumers and how they migrate or roll back when
needed. Consider check IDs, measurement and verdict schemas, severity and action
meaning, CLI exit behavior, configuration, and Go or wire APIs where affected.
Call out behavioral changes even if the serialized shape stays the same.

Identify dependencies, staged delivery, documentation changes, and any decision
needed from another component. A proposed design does not stabilize an
experimental interface or promise hardware support or an artifact release.

## Validation

Define observable acceptance criteria and the tests or evidence that will
demonstrate them. Distinguish planned tests from completed results, and unit or
mock evaluation from real hardware and consumer integration tests. State needed
hardware/driver/runtime coverage and remaining gaps where relevant. Explain why
a test class is unnecessary when that affects confidence in the design.

## Open questions and references

List unresolved choices, who needs to help resolve them, and links to public
issues, code, specifications, or other designs. Mark questions that block design
acceptance. Do not replace unresolved decisions with assumed approval.

## Implementation

Link implementation PRs and validation evidence as they become available. Keep
implemented, deferred, and unvalidated work explicit. Design acceptance and
implementation progress are separate; later user documentation describes the
supported behavior.
