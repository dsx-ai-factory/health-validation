# Contributing to NVIDIA Health Validation

Thank you for your interest in contributing. This document covers how to report
issues, propose changes, and submit code.

Before you participate, read and follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Code of Conduct Enforcement

Report Code of Conduct incidents to
[health-validation-maintainers@nvidia.com](mailto:health-validation-maintainers@nvidia.com).
This address is monitored by the project maintainers.

The maintainers will acknowledge a report within 3 business days. They will
provide a written outcome within 14 calendar days after acknowledgement. If the
review is not complete by then, they will provide a status update and a revised
target date.

Conduct is out of scope only when it occurred outside all project community
spaces and the person was not officially representing the project community.
If either condition is not met, the maintainers will handle the report under
the Code of Conduct.

## Reporting Bugs

Open a [GitHub Issue](../../issues/new?template=bug.yml). Include:

- The full JSON output of `health-validation run` (or the error message)
- GPU model and driver version (`nvidia-smi -q | head -20`)
- Kernel and OS (`uname -a`)
- How you invoked the tool and what you expected vs. what happened

For security vulnerabilities, see [`SECURITY.md`](SECURITY.md) — do not file a
public issue.

## Proposing Features or Checks

New GPU health checks are the most common contribution. Before implementing:

1. Open a [feature request](../../issues/new?template=feature.yml) describing
   the signal you want to evaluate and the hardware failure mode it detects.
2. Discuss threshold governance (is this check `FixedThreshold` or
   `DefaultThreshold`?) and which `Category` and `SchedulingCadence` it belongs to.
3. Wait for a maintainer to acknowledge the issue before writing code, to avoid
   wasted effort on proposals that don't align with project scope.

## Development Setup

**Prerequisites:** Go ≥ 1.23, Bazel ≥ 9 (see `.bazelversion`).

```bash
git clone https://github.com/dsx-ai-factory/health-validation.git
cd health-validation

# Build everything
bazel build //...

# Run all tests
bazel test //...

# Format (gofumpt + buildifier)
bazel run //tools/format

# Sync BUILD files after adding/removing Go files or imports
bazel run //tools/gazelle

# Go mod tidy (repo has two modules: catalog/ and toolkit/)
(cd catalog && go mod tidy)
(cd toolkit && go mod tidy)
```

> **Linux only:** The `toolkit/nvml/` package requires cgo and
> `libnvidia-ml.so.1`. On macOS, the NVML backend stubs out and `go test
> ./catalog/...` works, but `health-validation run` will exit with a backend
> error. Use a Linux VM or container to test NVML-dependent code.

## Adding a Health Check

The short version (see the architecture section in `README.md` for context):

1. **Define signals** on the relevant measurement struct in `catalog/<domain>/measurement.go`.
2. **Write a `CheckDefinition` factory** in the same package.
3. **Register it** in `catalog/checks.go` via `builtinChecks()`.
4. **Write table-driven tests** covering PASS, CRITICAL, and edge cases.
5. Run `bazel test //catalog/...` to confirm.

## Submitting a Pull Request

1. Fork the repository and create a branch off `main`.
2. Make your changes and run `bazel test //...` and `bazel run //tools/format`.
3. Open a PR using the [pull request template](.github/PULL_REQUEST_TEMPLATE.md).
4. Sign off every commit with `git commit --signoff` (see **Signing Your Work**
   below). PRs without sign-offs cannot be merged.
5. A maintainer will review and may request changes. Address feedback in new
   commits (do not force-push after review has started).

## Commit and PR Titles

Titles follow [Conventional Commits](https://www.conventionalcommits.org/):
`type(scope): subject`. CI checks this on every pull request through the
`commit-lint` check; the rules live in [`.commitlintrc.yaml`](.commitlintrc.yaml).

- Types: `build`, `chore`, `ci`, `docs`, `feat`, `fix`, `perf`, `refactor`,
  `revert`, `style`, `test`.
- Scope is optional and unrestricted — use the package or domain the change
  belongs to. `catalog`, `gpu`, `ci` and `container` are already in use.
- Keep both the PR title and your commit subjects conventional. Depending on
  the merge method, either one can become the subject on `main`.
- A leading capital is fine, so `fix: RDMA link-flap` is correct. Title-case and
  all-caps subjects are rejected: not `feat: Add Support For X`, not `fix: BROKEN`.
- Title a revert `revert: <original subject>`. The `Revert "..."` title GitHub's
  Revert button generates is not valid here.
- CI reads these rules from `main`, not from your branch. A PR that changes them
  is still judged by the current rules, so title it under those and use anything
  new in a follow-up PR.

Examples from this repository:

```
feat(gpu): add average power draw health check
fix(catalog): add GB300 to remaining GB200-only checks
```

## Code Style

- Go: [gofumpt](https://github.com/mvdan/gofumpt). Run `bazel run //tools/format`.
- Starlark/BUILD: buildifier (same `//tools/format` target).
- Struct field ordering: largest fields first to minimize padding; see
  `nogo`'s `fieldalignment` analyzer which gates CI.
- Comments: only when the *why* is non-obvious. Avoid restating what the code does.

## Signing Your Work

We require that all contributors "sign-off" on their commits. This certifies that
the contribution is your original work, or you have rights to submit it under the
same license, or a compatible license.

Any contribution which contains commits that are not Signed-Off will not be accepted.

To sign off on a commit you simply use the `--signoff` (or `-s`) option when
committing your changes:

```bash
$ git commit -s -m "feat(gpu): add cool feature"
```

This will append the following to your commit message:

```
Signed-off-by: Your Name <your@email.com>
```

The name and email must match a verified email on your GitHub account. For a series
of commits, `git rebase --signoff HEAD~N` retro-signs the top N commits.

### Full text of the Developer Certificate of Origin

Full text of the DCO (<https://developercertificate.org/>):

```
  Developer Certificate of Origin
  Version 1.1

  Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

  Everyone is permitted to copy and distribute verbatim copies of this
  license document, but changing it is not allowed.


  Developer's Certificate of Origin 1.1

  By making a contribution to this project, I certify that:

  (a) The contribution was created in whole or in part by me and I
      have the right to submit it under the open source license
      indicated in the file; or

  (b) The contribution is based upon previous work that, to the best
      of my knowledge, is covered under an appropriate open source
      license and I have the right under that license to submit that
      work with modifications, whether created in whole or in part
      by me, under the same open source license (unless I am
      permitted to submit under a different license), as indicated
      in the file; or

  (c) The contribution was provided directly to me by some other
      person who certified (a), (b) or (c) and I have not modified
      it.

  (d) I understand and agree that this project and the contribution
      are public and that a record of the contribution (including all
      personal information I submit with it, including my sign-off) is
      maintained indefinitely and may be redistributed consistent with
      this project or the open source license(s) involved.
```

## Questions

Open a [GitHub Discussion](../../discussions) or a
[task issue](../../issues/new?template=task.yml) for anything that doesn't fit
a bug or feature request.
