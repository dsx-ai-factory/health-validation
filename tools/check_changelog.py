# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Require a CHANGELOG.md entry on pull requests that change what users see.

A pull request needs an entry when its title type is feat, fix, or perf, or when
the title carries "!". An entry is a changelog line that links the pull request
(".../pull/<number>)") under the Unreleased section of the head's CHANGELOG.md
and that the base's CHANGELOG.md does not already have: the head must link the
pull request more times than the base does, so a link the base already carries,
such as a forward reference, does not count. A "!" pull request needs one under
"Breaking Changes", and a pull request that puts an entry under "Breaking
Changes" must carry "!". See RELEASE.md#compatibility.

Pull requests of other types, reverts, and pull requests labeled
"skip-changelog" need nothing. Neither the label nor a revert exempts a "!"
pull request or an entry under "Breaking Changes".

Pull requests targeting a release/* branch have no Unreleased section, so an
entry anywhere in the file counts.

The script reads only the strings it is given, never pull request code, so the
workflow can run it with pull_request_target.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter

REQUIRED_TYPES = frozenset({"feat", "fix", "perf"})
SKIP_LABEL = "skip-changelog"
BREAKING = "Breaking Changes"

_TITLE = re.compile(r"^(?P<type>[A-Za-z]+)(?:\([^)]*\))?(?P<bang>!)?:")
_PULL = re.compile(r"/pull/(\d+)\)")


def parse_title(title: str) -> tuple[str, bool] | None:
    """Returns (type, breaking) for a Conventional Commits title, else None."""
    if re.match(r"^[Rr]evert ", title):
        return ("revert", False)
    m = _TITLE.match(title.strip())
    if m is None:
        return None
    return (m.group("type").lower(), m.group("bang") is not None)


def entries(changelog: str, pr: int, whole_file: bool) -> dict[str, int]:
    """Counts the links to pr per "###" section.

    Scope is the Unreleased section, or the whole file when whole_file is set.
    """
    counts: Counter[str] = Counter()
    in_scope = whole_file
    section = ""
    for line in changelog.splitlines():
        if line.startswith("## "):
            if not whole_file:
                in_scope = line.startswith(("## [Unreleased]", "## \\[Unreleased\\]"))
            section = ""
            continue
        if line.startswith("### "):
            section = line[4:].strip()
            continue
        if not in_scope:
            continue
        counts[section] += sum(1 for n in _PULL.findall(line) if int(n) == pr)
    return dict(counts)


def check(
    title: str,
    labels: list[str],
    pr: int,
    base_ref: str,
    base_changelog: str,
    head_changelog: str,
) -> list[str]:
    """Returns the failures; empty means the pull request passes."""
    parsed = parse_title(title)
    if parsed is None:
        return []  # commit-lint reports a malformed title.
    kind, breaking = parsed

    whole = base_ref.startswith("release/")
    base = entries(base_changelog, pr, whole)
    head = entries(head_changelog, pr, whole)
    needs_entry = breaking or (kind in REQUIRED_TYPES and SKIP_LABEL not in labels)

    # An entry is a link this pull request adds. The base may already link the
    # pull request, such as a forward reference from another entry, so only a
    # net increase counts: a link that moves between sections adds nothing.
    credited = sum(head.values()) > sum(base.values())
    breaking_new = head.get(BREAKING, 0) > base.get(BREAKING, 0)
    breaking_credit = credited and breaking_new
    errors: list[str] = []

    if breaking:
        if not breaking_credit:
            errors.append(
                f"The title marks a breaking change (!), so CHANGELOG.md needs an entry "
                f'under "{BREAKING}" linking /pull/{pr}. List only the parts of the '
                f"pull request that break a public surface there, each with what a "
                f"consumer must change, and the other parts under Changes, Features, or "
                f"Fixes. See RELEASE.md#compatibility."
            )
    elif breaking_new:
        errors.append(
            f'CHANGELOG.md lists /pull/{pr} under "{BREAKING}", but the title has no "!". '
            f"Add ! to the title, or move the entry. See RELEASE.md#compatibility."
        )

    if needs_entry and not credited and not errors:
        errors.append(
            f"A {kind} pull request needs a CHANGELOG.md entry. Add one under "
            f"Unreleased linking /pull/{pr}, or add the {SKIP_LABEL} label if the "
            f"change has no user-visible effect."
        )
    return errors


def main() -> int:
    ap = argparse.ArgumentParser(description=(__doc__ or "").splitlines()[0])
    ap.add_argument("--title", required=True)
    ap.add_argument("--labels", default="[]", help="JSON array of label names")
    ap.add_argument("--pr", type=int, required=True)
    ap.add_argument("--base-ref", required=True)
    ap.add_argument("--base-changelog", required=True)
    ap.add_argument("--head-changelog", required=True)
    args = ap.parse_args()

    def read(path: str) -> str:
        with open(path, encoding="utf-8") as f:
            return f.read()

    errors = check(
        args.title,
        json.loads(args.labels),
        args.pr,
        args.base_ref,
        read(args.base_changelog),
        read(args.head_changelog),
    )
    for e in errors:
        print(f"::error file=CHANGELOG.md::{e}")
    if not errors:
        print("Changelog check passed.")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
