#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Point the Fern site at one ref-backed version per release line.

Each MAJOR.MINOR line becomes one version, built by Fern from that line's
highest final vX.Y.Z tag; release candidates are skipped. A tag without
fern/docs.yml and docs/index.yml predates the Fern site and is skipped too,
because Fern builds a ref-backed version from the Fern folder at that ref.

The version list in <fern-dir>/docs.yml is replaced with those entries, newest
first, so Fern serves the newest line at unversioned URLs and older lines under
/vX.Y/.

With no eligible tag docs.yml is left untouched, so the site is published from
the checked-out docs.

Usage: fern_docs_versions.py [--repo DIR] [--fern-dir DIR]
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

FINAL_TAG = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
REQUIRED_FILES = ("fern/docs.yml", "docs/index.yml")
# The committed working-tree version list that the release versions replace.
WORKING_TREE_VERSIONS = """\
versions:
  - display-name: Latest
    path: ../docs/index.yml
    slug: latest
"""


def has_files(repo: Path, tag: str) -> bool:
    return all(
        subprocess.run(
            ["git", "-C", str(repo), "cat-file", "-e", f"{tag}:{path}"],
            capture_output=True,
        ).returncode
        == 0
        for path in REQUIRED_FILES
    )


def release_lines(repo: Path) -> list[tuple[str, str]]:
    """Return (slug, tag) for each Fern-bearing release line, newest first."""
    tags = subprocess.run(
        ["git", "-C", str(repo), "tag", "--list", "v*"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout.split()
    newest: dict[tuple[int, int], tuple[int, str]] = {}
    for tag in tags:
        match = FINAL_TAG.match(tag)
        if not match or not has_files(repo, tag):
            continue
        major, minor, patch = (int(part) for part in match.groups())
        current = newest.get((major, minor))
        if current is None or patch > current[0]:
            newest[(major, minor)] = (patch, tag)
    return [
        (f"v{major}.{minor}", tag)
        for (major, minor), (_, tag) in sorted(newest.items(), reverse=True)
    ]


def versions_block(lines: list[tuple[str, str]]) -> str:
    block = ["versions:"]
    for slug, tag in lines:
        block += [
            f"  - display-name: {tag}",
            f"    ref: {tag}",
            f"    slug: {slug}",
        ]
    return "\n".join(block) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--repo", type=Path, default=Path("."), help="git checkout")
    parser.add_argument(
        "--fern-dir",
        type=Path,
        default=Path("fern"),
        help="Fern configuration directory",
    )
    args = parser.parse_args()

    docs_yml = args.fern_dir / "docs.yml"
    config = docs_yml.read_text()
    if config.count(WORKING_TREE_VERSIONS) != 1:
        print(
            f"{docs_yml}: expected exactly one working-tree version list:\n"
            + WORKING_TREE_VERSIONS,
            file=sys.stderr,
        )
        return 1

    lines = release_lines(args.repo)
    if not lines:
        print("No release tag has Fern docs; publishing the checked-out docs.")
        return 0

    for slug, tag in lines:
        print(f"{slug}: {tag}")
    docs_yml.write_text(config.replace(WORKING_TREE_VERSIONS, versions_block(lines)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
