# Contribution intake

On new issue and PR openings, including draft PRs, reviewed code from `main`
fetches current GitHub metadata and maintains one `affiliation/*` label.
Classification starts disabled. It needs only GitHub access and private reviewed
affiliation evidence. The existing GitHub Slack subscription owns notifications
separately; this workflow has no Slack transport or configuration.

## Labels and shared views

The labels are `affiliation/nvidia`, `affiliation/external`,
`affiliation/unverified` and `affiliation/bot`. Only those four labels may be added
or removed. All other labels and assignees are preserved. The Task / Question form
also applies the existing `needs-triage` label.

- [Open external items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Fexternal)
- [Open unverified items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Funverified)
- [Open items without an affiliation label](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20-label%3Aaffiliation%2Fnvidia%20-label%3Aaffiliation%2Fexternal%20-label%3Aaffiliation%2Funverified%20-label%3Aaffiliation%2Fbot)
- [Open items needing triage](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aneeds-triage)
- [Unassigned open issues](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aissue%20is%3Aopen%20no%3Aassignee)
- [Ready PRs without approval](https://github.com/dsx-ai-factory/health-validation/pulls?q=is%3Apr%20is%3Aopen%20draft%3Afalse%20-review%3Aapproved)
- [All open issues and PRs](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen)

Use the all-open view to include drafts, approved PRs awaiting merge and items
awaiting replies. Maintainers review acknowledgements and ownership manually;
unassigned status and review filters alone cannot establish those facts.

## Private affiliation evidence

Only reviewed evidence tied to the author's numeric GitHub account ID establishes
NVIDIA or external status. Access roles, CODEOWNERS, team or organization
membership, `author_association`, labels, profile company and commit email cannot
establish employment. Bot status comes from the author's GitHub account type.
Missing, malformed, conflicting, expired or future-dated relevant evidence leaves
humans unverified. A numeric ID survives a valid login rename; a reused login
cannot transfer another account's affiliation.

Keep evidence and review history in private storage, outside source, logs and
workflow artifacts. Supply one current record per author in the Actions secret
`HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON`. Every field below is required;
these are synthetic values and reviewers must choose current review/expiry dates:

```json
[
  {
    "author_id": 123456,
    "login": "example-contributor",
    "affiliation": "external",
    "reviewed_by": "authorized-reviewer",
    "evidence_ref": "private-record-reference",
    "reviewed_at": "2026-10-19T12:00:00Z",
    "expires_at": "2026-10-26T12:00:00Z",
    "revision": "review-1"
  }
]
```

Missing, malformed or non-array registry JSON produces a generic warning and
leaves humans unverified. An empty array also leaves every human unverified.
Maintainers review evidence and corrections manually; there is no directory sync.

## Supervised activation and rollback

1. Review and merge the disabled workflow and scripts into `main`.
2. Create the four affiliation labels and provide the private reviewed registry.
   Agree who reviews evidence, expiry and contributor corrections before use.
3. Verify the workflow can read items and add/remove issue labels in this fixed
   repository. The CLI also requires `HEALTH_VALIDATION_APPLY_LABELS=true` exactly;
   the workflow supplies that opt-in inside its disabled job.
4. Set the repository variable `HEALTH_VALIDATION_INTAKE_ENABLED` to `true` exactly
   for a supervised pilot. Missing or any other value keeps the job disabled.
5. Check normal pilot openings for reviewed NVIDIA/external authors, unverified
   authors, Bot accounts and draft PRs. Verify unrelated labels and assignees and
   review generic failures before wider activation.

To roll back, remove `HEALTH_VALIDATION_INTAKE_ENABLED` or set it to `false`, and
cancel an in-progress intake run if needed. Existing labels remain; maintainers
can correct affiliation labels manually. The separate native Slack feed remains
configured independently.

The workflow handles openings only. It has no backfill or later affiliation
refresh, assignment, priority, acknowledgement or response tracking. Labels do
not establish a response SLA or change published support policy; engineering
triage and follow-up remain manual.
