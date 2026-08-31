## Summary

<!-- What does this PR do? Why? Link the issue it closes: "Closes #123" -->

## Changes

<!-- Bullet list of meaningful changes. Skip if obvious from the diff. -->

## Type

- [ ] Bug fix
- [ ] New check / check enhancement
- [ ] Refactor / cleanup
- [ ] Documentation
- [ ] Dependency update
- [ ] Other: ___

## Test plan

- [ ] `bazel test //...` passes
- [ ] `bazel run //tools/format` passes (no diff)
- [ ] New/changed checks have table-driven tests covering PASS and CRITICAL cases
- [ ] Tested on hardware (if applicable — GPU model: ___)

## Checklist

- [ ] PR title follows Conventional Commits (`type(scope): subject`)
- [ ] Commit messages are self-contained and describe *why*, not just *what*
- [ ] No internal tooling references, URLs, or credentials in the diff
- [ ] `go mod tidy` run in affected module(s) (`catalog/` and/or `toolkit/`)
- [ ] Struct fields ordered for minimal padding (nogo `fieldalignment` gating CI)
