# Contribution intake

On new issue and PR openings, including drafts, trusted code from `main` reads
the current author and maintains an `affiliation/*` label. GitHub's item type
distinguishes issues from PRs. The pilot starts disabled. The existing GitHub
Slack subscription handles notifications separately.

## Labels and shared views

The labels are `affiliation/nvidia`, `affiliation/external`,
`affiliation/unverified` and `affiliation/bot`. Only those four labels may be added
or removed. All other labels and assignees are preserved. Unverified means the
author's affiliation needs review, not that the author is external. Bots have
their own label.

- [Open NVIDIA issues](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aissue%20is%3Aopen%20label%3Aaffiliation%2Fnvidia)
- [Open external issues](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aissue%20is%3Aopen%20label%3Aaffiliation%2Fexternal)
- [Open NVIDIA PRs](https://github.com/dsx-ai-factory/health-validation/pulls?q=is%3Apr%20is%3Aopen%20label%3Aaffiliation%2Fnvidia)
- [Open external PRs](https://github.com/dsx-ai-factory/health-validation/pulls?q=is%3Apr%20is%3Aopen%20label%3Aaffiliation%2Fexternal)
- [Open unverified items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Funverified)
- [Open items without an affiliation label](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20-label%3Aaffiliation%2Fnvidia%20-label%3Aaffiliation%2Fexternal%20-label%3Aaffiliation%2Funverified%20-label%3Aaffiliation%2Fbot)
- [All open issues and PRs](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen)

PR views include drafts for intake. Review metrics may exclude drafts separately.
Check the unverified and unlabeled views for unresolved identities or failed runs.

## How affiliation is determined

For human authors, the workflow requests
`GET /orgs/{org}/memberships/{username}` from GitHub, checking `dsx-ai-factory`
first and `NVIDIA` if needed. Active membership in either organization is the
configured positive signal for NVIDIA affiliation. Both memberships are not
required. The response must match the author's numeric GitHub ID and the expected
organization ID. Pending invitations do not qualify.

This classifies affiliation for triage. It does not grant organization membership,
repository access, review authority or priority. Repository collaborators,
CODEOWNERS, `author_association`, profile company and commit email are not used
to decide affiliation. Bot status comes from GitHub's account type.

No membership match means unresolved, not external. Missing credentials and
failed lookups produce the same unresolved result unless an independently
reviewed exception applies. A current external exception conflicting with a
membership match, or malformed relevant exceptions, leaves the author unverified.
Membership checks use bounded retries; rate-limit delays over ten seconds stop
that lookup so classification can continue.

## Private configuration and exceptions

Configure an approved GitHub App installed in both organizations with
organization **Members: read** permission. Set the repository variable
`HEALTH_VALIDATION_MEMBERSHIP_APP_CLIENT_ID` and secret
`HEALTH_VALIDATION_MEMBERSHIP_APP_PRIVATE_KEY`. The pinned token action creates
separate installation tokens requesting only membership-read access. Tokens are
normally revoked after the job and expire after one hour. The repository's
`GITHUB_TOKEN` is used separately for reading items and updating labels.

If configuration is missing or token creation fails, classification still runs
with available evidence. Full job cancellation or timeout can prevent labeling.
Private membership visibility must be verified using the actual App installation
credentials; a successful check with a maintainer's personal account is not enough.

The optional secret `HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON` holds exceptions
for confirmed external authors or NVIDIA authors not recognized by the two
organization checks. A maintainer must verify affiliation independently, record
the evidence privately, and bind the decision to the numeric GitHub account ID.
Free-text profile information and absence from a membership list are insufficient.
No exception record is needed for an active membership match.

Keep evidence and review history outside public source, logs and artifacts. These
are synthetic values; every field is required and review/expiry dates must be current:

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

Missing or empty exception JSON means no exceptions. Malformed JSON or a non-array
value produces a generic warning and leaves humans unverified. Expired or
future-dated exceptions cannot establish affiliation. A valid numeric ID survives
a login rename; reusing a login cannot transfer another account's exception.

Before uploading a registry, check its format and current dates locally:

```shell
node tools/contributor-intake/check-registry.mjs /private/path/approved-registry.json
```

The check rejects invalid, expired, future-dated, conflicting or oversized records
without printing their contents. It does not verify evidence or approve decisions.
Keep the reviewed source file and history private; the secret is a runtime copy.
Name an exception owner and refresh records before expiry. Labels reflect the
last classification. Changed membership or expired records do not automatically
relabel older items. No roster or directory synchronization is required.

## Corrections and failed runs

After correcting an exception, membership or failed run, start a new
**Contribution intake pilot** run from `main` with the item type and number. This
also handles a missed opening or an item created before activation. It reads
current evidence, corrects only affiliation labels, and leaves closed items closed:

```shell
gh workflow run contributor-intake.yml --repo dsx-ai-factory/health-validation \
  --ref main -f kind=issue -f number=123
```

GitHub requests retry transient failures up to three attempts. Label-write
rate-limit delays over one minute stop the run instead of retrying early.
A failed write can leave a partial correction; start a new run after it is fixed.

## Supervised activation and rollback

1. Review and merge the disabled workflow and scripts into `main`.
2. Create the four affiliation labels and configure the approved membership App.
   Verify active private-member responses in each organization using the actual
   installation credentials. Name an owner for exceptions and corrections.
3. Verify the workflow can read items and add/remove affiliation labels on both
   issues and PRs in this fixed repository. The job needs `issues: write` and
   `pull-requests: write`. The CLI also requires
   `HEALTH_VALIDATION_APPLY_LABELS=true` exactly;
   the workflow supplies that opt-in inside its disabled job.
4. Check applicable Actions event policies permit this trusted `pull_request_target`
   workflow. Set `HEALTH_VALIDATION_INTAKE_ENABLED` to `true` exactly
   for a supervised pilot. Missing or any other value keeps the job disabled.
5. Check normal openings for DSX-only and NVIDIA-only members, reviewed external
   authors, unknown authors, Bot accounts and draft PRs. Check conflicts and token
   failures as well as unrelated labels and assignees before wider activation.

To roll back, remove `HEALTH_VALIDATION_INTAKE_ENABLED` or set it to `false`, and
cancel an in-progress intake run if needed. Existing labels remain; maintainers
can correct affiliation labels manually. The separate native Slack feed remains
configured independently.

The five-minute job uses a hosted runner and serializes repeated work for each
item. It does not impose an aggregate limit across distinct issues and PRs;
check the unlabeled queue for canceled or failed runs. It has no scheduled
backfill, assignment, priority, acknowledgement or response tracking. Labels do
not establish a response SLA; engineering triage and follow-up remain manual.
