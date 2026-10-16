# Health Validation release notes proposal

**Draft for team review.** Agree on the format and the example wording before updating the changelog or published release descriptions. This proposal does not change a release, tag, artifact, workflow, or documentation-site navigation.

## Recommended approach

Use release notes organized around the decisions operators and integrators need to make: what changed, whether an upgrade changes verdicts or required actions, which components and environments are covered, and how to obtain the exact release.

Health Validation applies catalog checks to one node's measurements and returns a structured verdict. Downstream systems perform scheduling and remediation. A change to severity, check applicability or recommended actions can affect operational decisions even when the JSON schema stays the same. Those changes need their own section.

Use NVIDIA Resiliency Extension as the editorial reference and CRE for artifact and installation clarity. Keep the existing independently versioned catalog and toolkit tags. Identify the actual catalog bundled into the CLI separately from the catalog required by the published toolkit Go module.

## References and proposed adaptations

| Reference | Useful pattern | Adaptation for Health Validation |
| --- | --- | --- |
| [CRE v0.1.0](https://github.com/NVIDIA/cluster-readiness-engine/releases/tag/v0.1.0) and [v0.7.0](https://github.com/NVIDIA/cluster-readiness-engine/releases/tag/v0.7.0) | Release identity, artifact details, installation instructions and curated highlights in later releases | Include the versioned image and build metadata; include only distribution methods Health Validation actually provides. |
| [NVIDIA Resiliency Extension v0.7.0](https://nvidia.github.io/nvidia-resiliency-ext/release-notes.html#nvidia-resiliency-extension-v0-7-0) | Capability groups, explicit migrations, experimental status and known limitations | Make this the main editorial model for feature releases. |
| [DCGM](https://docs.nvidia.com/datacenter/dcgm/latest/release-notes/changelog.html) and [GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/26.7/release-notes.html) | Precise diagnostic changes, component versions, applicability and workarounds | Describe changed check behavior and separate validated hardware coverage from experimental declarations. |

## Proposed structure

| Section | What the reader should learn |
| --- | --- |
| Summary and release information | Main outcome, publication date, toolkit tag, bundled catalog, Go module catalog dependency, schema and image. |
| Upgrade actions and breaking changes | Exact old and new values, affected consumers and required migration. |
| Changes to checks and verdicts | Check IDs, changed triggers or thresholds, severity, remediation, applicability and operational effect. |
| Collection and integrations | CLI, configuration, sensors, metrics and Go APIs; experimental or library-only availability. |
| Fixes and security | User-visible corrections and relevant shipped dependency updates. |
| Compatibility and known limitations | Validated versus experimental hardware, prerequisites, known issues and workarounds. |
| Installation and references | Numbered image, version verification, component-specific module installation, versioned documentation and full changes. |

Use the full structure for feature releases and omit empty sections for patches. See the [authoring template](template.md) and the [proposed v0.3.1 example](v0.3.1-example.md).

For a feature-release example, v0.3.0 should explain that Xid 144–150 changed from CRITICAL on any occurrence to distinguishing Fatal CRITICAL from Nonfatal WARNING with no action. That check-behavior change belongs alongside the schema migration, with its own explanation of operational impact. [Change #52](https://github.com/dsx-ai-factory/health-validation/pull/52).

## Why the example separates CLI and Go consumers

The v0.3.0 release CI records bundled catalog `v0.3.1-0.20261008220429-a088431d0419`, while its published toolkit Go module requires catalog `v0.3.0`. The v0.3.1 CLI and toolkit dependency both use catalog `v0.3.1`. Both releases use verdict schema `0.3`.

Catalog v0.3.1 contains affected-entity and Fatal/Nonfatal Xid fixes already present in the v0.3.0 CLI. Therefore an unqualified statement that v0.3.1 has no behavior or API changes does not adequately describe an upgrade from catalog v0.3.0. Consumers' selected module graphs determine which catalog they actually use.

Evidence: [v0.3.0 CI metadata](https://github.com/dsx-ai-factory/health-validation/actions/runs/37855754451), [v0.3.1 CI metadata](https://github.com/dsx-ai-factory/health-validation/actions/runs/38479758823), [v0.3.0 toolkit dependency](https://github.com/dsx-ai-factory/health-validation/blob/toolkit/v0.3.0/toolkit/go.mod), [v0.3.1 toolkit dependency](https://github.com/dsx-ai-factory/health-validation/blob/toolkit/v0.3.1/toolkit/go.mod), and [catalog comparison](https://github.com/dsx-ai-factory/health-validation/compare/catalog/v0.3.0...catalog/v0.3.1).

## Feedback requested

1. Confirm whether the section order and shorter patch format meet operator and integrator needs.
2. Validate the proposed example, especially the distinction between CLI/container behavior and Go module behavior.
3. Agree whether the first follow-up should cover v0.3.1 alone or also correct missing final v0.3.0 fixes and maintenance-release entries.

## Adoption after review

Keep the existing changelog as the canonical reviewed narrative and reuse it in GitHub Releases. The documentation site can render or link to that content. Do not maintain independently edited copies.

Once the format and wording are agreed, prepare a separate documentation PR for the selected changelog and process changes. Editing an existing GitHub release description is a separate publication step using the approved text. Neither requires a new version, tag or rebuilt artifact.

Release automation and extensive historical backfilling can follow separately. This proposal can be reviewed alongside release preparation without becoming a release blocker.
