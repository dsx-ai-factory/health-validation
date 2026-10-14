// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Conventional Commits rules, checked on pull requests by
// .github/workflows/commit-lint.yml.
//
// JavaScript rather than YAML because `ignores` entries must be functions.
module.exports = {
  extends: ['@commitlint/config-conventional'],

  // commitlint's CLI ignores ten message shapes by default (Merge branch ...,
  // fixup!, bare semver, and more), exiting 0 with no rule evaluated and no
  // visible difference in the log. Turn that off and re-add only the one shape
  // we want exempt, so the other nine stay linted.
  defaultIgnores: false,
  ignores: [
    // GitHub's Revert button prefills this title, so exempting it keeps a
    // revert from needing a retitle before it can merge. Same pattern
    // commitlint itself uses, from @commitlint/is-ignored defaults.
    //
    // Deliberately unanchored: any title starting with `Revert ` or `revert `
    // skips every rule, `revert lol` included. Narrowing it to `Revert "..."`
    // would reject reworded reverts and diverge from upstream commitlint,
    // which matters while the plan is to move this job onto the shared DSX
    // action. `Reapply "..."`, which a revert of a revert produces, is not
    // exempt.
    (message) => /^(R|r)evert (.*)/.test(message),
  ],

  helpUrl:
    'https://github.com/dsx-ai-factory/health-validation/blob/main/CONTRIBUTING.md#commit-and-pr-titles',

  rules: {
    // config-conventional inherits a 100-character limit, which rejects
    // Dependabot's github-actions titles: for a long action path the generated
    // `bump <path> from X to Y` is already 100 characters before any type
    // prefix, so no prefix fits and the pull request cannot be titled
    // conformantly. 120 clears the current worst case with headroom.
    'header-max-length': [2, 'always', 120],

    // config-conventional also bans sentence-case, which rejects any subject
    // starting with a capital. This repo's vocabulary is acronyms (RDMA, XID,
    // NVML, GB300), so a leading capital is normal here. Title-case and all-caps
    // subjects stay banned.
    'subject-case': [2, 'never', ['start-case', 'pascal-case', 'upper-case']],
  },
};
