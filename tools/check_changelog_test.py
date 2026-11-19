# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import check_changelog as cc  # noqa: E402

BASE = """# Changelog

## [Unreleased]

### Breaking Changes

- Old break. ([#1](https://github.com/o/r/pull/1))

### Features

- Old feature. ([#2](https://github.com/o/r/pull/2))

## [v0.3.0] - 2026-10-05

### Features

- Shipped. ([#7](https://github.com/o/r/pull/7))
"""


def with_entry(section: str, pr: int, text: str = BASE) -> str:
    line = f"- New. ([#{pr}](https://github.com/o/r/pull/{pr}))\n"
    head, _, rest = text.partition(f"### {section}\n\n")
    return f"{head}### {section}\n\n{line}{rest}"


class TitleTest(unittest.TestCase):
    def test_parse(self):
        cases = {
            "feat(gpu): add x": ("feat", False),
            "fix!: y": ("fix", True),
            "refactor(toolkit)!: z": ("refactor", True),
            "Fix: Capital": ("fix", False),
            'Revert "feat: x"': ("revert", False),
            "no type": None,
        }
        for title, want in cases.items():
            with self.subTest(title):
                self.assertEqual(cc.parse_title(title), want)


class CheckTest(unittest.TestCase):
    def run_check(self, title, head, labels=(), base_ref="main", base=BASE, pr=10):
        return cc.check(title, list(labels), pr, base_ref, base, head)

    def test_exempt_types_pass_without_an_entry(self):
        for t in (
            "build(deps): bump x",
            "chore: y",
            "ci: z",
            "docs: w",
            "test: v",
            "style: u",
            "refactor: t",
        ):
            with self.subTest(t):
                self.assertEqual(self.run_check(t, BASE), [])

    def test_feat_fix_perf_need_an_entry(self):
        for t in ("feat: a", "fix(xid): b", "perf: c"):
            with self.subTest(t):
                self.assertEqual(len(self.run_check(t, BASE)), 1)

    def test_entry_in_an_unreleased_section_satisfies(self):
        for section in ("Breaking Changes", "Features"):
            with self.subTest(section):
                title = "feat!: a" if section == "Breaking Changes" else "feat: a"
                self.assertEqual(self.run_check(title, with_entry(section, 10)), [])

    def test_entry_in_a_released_section_does_not_count(self):
        head = BASE.replace("pull/7)", "pull/10)")
        self.assertEqual(len(self.run_check("feat: a", head)), 1)

    def test_link_must_name_this_pr(self):
        self.assertEqual(len(self.run_check("feat: a", with_entry("Features", 11))), 1)

    def test_number_is_matched_whole(self):
        self.assertEqual(len(self.run_check("feat: a", with_entry("Features", 100))), 1)

    def test_preexisting_entry_does_not_count(self):
        base = with_entry("Features", 10)
        self.assertEqual(len(self.run_check("feat: a", base, base=base)), 1)

    def test_label_exempts_a_non_breaking_pr(self):
        self.assertEqual(self.run_check("fix: a", BASE, labels=["skip-changelog"]), [])

    def test_breaking_needs_a_breaking_entry(self):
        self.assertEqual(len(self.run_check("feat!: a", with_entry("Features", 10))), 1)
        self.assertEqual(
            self.run_check("feat!: a", with_entry("Breaking Changes", 10)), []
        )

    def test_label_never_exempts_breaking(self):
        self.assertEqual(
            len(self.run_check("fix!: a", BASE, labels=["skip-changelog"])), 1
        )

    def test_breaking_applies_to_exempt_types(self):
        self.assertEqual(len(self.run_check("refactor(x)!: a", BASE)), 1)

    def test_breaking_entry_without_bang_fails(self):
        self.assertEqual(
            len(self.run_check("feat: a", with_entry("Breaking Changes", 10))), 1
        )

    def test_multiple_sections_for_one_pr(self):
        head = with_entry("Features", 10, with_entry("Breaking Changes", 10))
        self.assertEqual(self.run_check("feat!: a", head), [])

    def test_revert_and_unparseable_pass(self):
        self.assertEqual(self.run_check('Revert "feat: x"', BASE), [])
        self.assertEqual(self.run_check("no type", BASE), [])

    def test_release_branch_counts_any_section(self):
        head = with_entry("Features", 10).replace(
            "## [Unreleased]", "## [v0.3.1] - 2026-11-01", 1
        )
        self.assertEqual(self.run_check("fix: a", head, base_ref="release/v0.3"), [])
        self.assertEqual(len(self.run_check("fix: a", head)), 1)


if __name__ == "__main__":
    unittest.main()
