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
- [ ] Every commit has a DCO `Signed-off-by` trailer
- [ ] Every commit is cryptographically signed and shows Verified on GitHub
- [ ] No internal tooling references, URLs, or credentials in the diff
- [ ] `bazel run //tools/go -- mod tidy` run in affected modules (`agentkit/`, `catalog/`, `collect/`, `toolkit/`)
- [ ] Struct fields ordered for minimal padding (nogo `fieldalignment` gating CI)
