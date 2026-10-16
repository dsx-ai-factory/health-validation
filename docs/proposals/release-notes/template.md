# Health Validation release notes authoring template

Use this structure for a toolkit CLI and container release. Replace every bracketed field from the final release commit and artifact metadata. Catalog-only releases should use a Catalog title, module installation instructions, and their effect on dependent toolkit releases. They should not imply a new container was published.

For a small patch, retain the summary, affected versions, actual fixes, upgrade implications and installation/documentation links. Omit empty sections. The [v0.3.1 example](v0.3.1-example.md) demonstrates the shorter form. This template is proposed for review and is not yet adopted release policy.

---

# NVIDIA Health Validation v[version]

[One paragraph describing the release's main outcome and who benefits. State whether upgrading requires a consumer, configuration or deployment change.]

| Release information | Value |
| --- | --- |
| Published | [Final release publication date, with timezone if necessary] |
| CLI and container tag | `toolkit/v[version]` |
| Compared with | `toolkit/v[previous-final-version]` |
| Bundled catalog reported by the CLI | `[exact catalogVersion from released image]` |
| Catalog required by the toolkit Go module | `[catalog requirement from released toolkit/go.mod]` |
| Verdict `schema_version` | `[schema version at release commit]` |
| Container image | `ghcr.io/dsx-ai-factory/health-validation:[version]` |
| Platforms | [Actual published image platforms] |

## Highlights

- [User outcome, affected feature or check, and PR link.]
- [Second significant outcome, if applicable.]
- [Third significant outcome, if applicable.]

## Upgrade actions and breaking changes

| Affected consumer | Previous behavior or value | New behavior or value | Required action |
| --- | --- | --- | --- |
| [Verdict parser, scheduler, Go caller or deployment] | [Exact old field, flag, API or default] | [Exact replacement or behavior] | [Specific migration and documentation link] |

[State what is unchanged only when it helps readers assess this upgrade. Distinguish CLI/container users from Go module consumers when their bundled dependencies differ.]

## Changes to checks and verdicts

| Check or family | Previous behavior | New behavior | Applicability and operator impact |
| --- | --- | --- | --- |
| `[check_id]` | [Trigger, threshold, severity or action before] | [Trigger, threshold, severity or action after] | [Architecture, context, cadence, prerequisites and downstream consequence; PR link] |

[For threshold changes, include units, architecture, old/new defaults and treatment of explicit overrides. For removed or renamed checks, include the migration of configuration, disabled IDs, alerts and dashboards.]

## Collection and integrations

- **CLI and configuration:** [Flags, YAML, defaults and deployment requirements that changed.]
- **Observability:** [Metric name, labels, units, cardinality or publication semantics that changed.]
- **Go integration:** [New or changed APIs and migration.]
- **Experimental capabilities:** [Maturity, exact access path, whether available in the stock CLI/image, and remaining limits.]

## Fixes and security

- [Observed symptom and corrected result, with affected surface and PR.]
- [Shipped dependency or base-image update, old/new versions and applicable advisory when verified. Do not infer a clean vulnerability scan from a version bump.]

## Compatibility and known limitations

- **Hardware:** [Validated architectures and separately identified experimental declarations.]
- **Runtime:** [Supported OS/architectures and changed driver, NVML, journal or host-view prerequisites. Link the versioned integration guide.]
- **Known issue:** [Affected condition, consequence, workaround and issue link.]
- **Support lifecycle:** [Any transition or EOL date required by current policy; link the authoritative support table.]

[Reference persistent contracts such as skipped-check handling through documentation. Repeat a limitation in the release body when it materially affects adoption of this release. Do not write “no known issues” without a release review supporting that statement.]

## Install and verify

```bash
docker pull ghcr.io/dsx-ai-factory/health-validation:[version]
docker run --rm ghcr.io/dsx-ai-factory/health-validation:[version] version --output json
```

The metadata command does not perform a node health check. Link to the integration guide at the final toolkit tag for GPU, sysfs and journal access.

For an existing Go consumer module, select the component it uses:

```bash
go get github.com/dsx-ai-factory/health-validation/toolkit@v[version]
go get github.com/dsx-ai-factory/health-validation/catalog@v[catalog-version]
```

[State the tested or supported dependency combination. A toolkit `require` is not a guarantee of the consumer's final module selection. Record a registry digest only after verifying it against the published image.]

Use a numbered image tag or verified digest for a released deployment. The `latest` image follows development on `main`.

## Documentation and support

- Verdict schema: [insert an absolute URL at the producer's final tag or commit].
- Integration and metrics guide: [insert an absolute URL at the final toolkit tag].
- Remediation contract: [insert an absolute URL at the final toolkit tag].
- [Support policy](https://github.com/dsx-ai-factory/health-validation/blob/main/SUPPORT.md)
- [Security reporting](https://github.com/dsx-ai-factory/health-validation/blob/main/SECURITY.md)
- Full toolkit changes: [insert the comparison from the previous toolkit final tag to this toolkit final tag].
- Full catalog changes: [insert the comparison between applicable catalog versions when different].

---

## Publication checks for the author

Reconcile the narrative against the final target commit, including QA and backport changes. Confirm the CLI's bundled versions from release metadata or the released image, and the Go dependency separately. Check that every claimed feature is on the described consumption path. Use absolute, versioned documentation URLs and the prior final tag of the same module. Record historical facts accurately when editing an old release.

Keep this reviewed narrative in the existing changelog and publish the same content to GitHub Releases. Add an automatically generated change inventory only as a secondary appendix. This template does not require new release automation or changes to the artifact pipeline.
