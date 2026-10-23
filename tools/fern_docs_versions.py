#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Stage one Fern docs version per toolkit release line.

The toolkit release tag (toolkit/vX.Y.Z) defines the docs version, because the
toolkit is what operators deploy and it pins the catalog it ships. Each
MAJOR.MINOR line becomes one version, built from that line's highest final
tag; release candidates are skipped. A tag whose tree has no docs/index.yml
predates the Fern site and is skipped too.

For each version, the tag's docs/ tree is written to
<fern-dir>/versions/vX.Y/, and the product in <fern-dir>/docs.yml is pointed at
those copies. The newest line is first, so Fern serves it as the default at
unversioned URLs and older lines under /vX.Y/.

Files are read from git objects rather than extracted from an archive, and
only regular files are written. The publish job still runs check-docs-paths
over the result before Fern sees it.

With no eligible tag the Fern directory is left untouched, so the site is
published unversioned from the checked-out docs.

Usage: fern_docs_versions.py [--repo DIR] [--fern-dir DIR]
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

TAG_PREFIX = "toolkit/"
FINAL_TAG = re.compile(r"^toolkit/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
DOCS_DIR = "docs"
NAV_FILE = "index.yml"
VERSIONS_DIR = "versions"
# The product line in fern/docs.yml that the version list replaces.
UNVERSIONED_PATH = "    path: ../docs/index.yml"
REGULAR_FILE_MODES = {"100644", "100755"}


def git(repo: Path, *args: str) -> bytes:
    return subprocess.run(
        ["git", "-C", str(repo), *args], check=True, capture_output=True
    ).stdout


def has_nav(repo: Path, tag: str) -> bool:
    result = subprocess.run(
        ["git", "-C", str(repo), "cat-file", "-e", f"{tag}:{DOCS_DIR}/{NAV_FILE}"],
        capture_output=True,
    )
    return result.returncode == 0


def release_lines(repo: Path) -> list[tuple[str, str]]:
    """Return (slug, tag) for each docs-bearing release line, newest first."""
    newest: dict[tuple[int, int], tuple[int, str]] = {}
    for tag in git(repo, "tag", "--list", f"{TAG_PREFIX}v*").decode().split():
        match = FINAL_TAG.match(tag)
        if not match or not has_nav(repo, tag):
            continue
        major, minor, patch = (int(part) for part in match.groups())
        current = newest.get((major, minor))
        if current is None or patch > current[0]:
            newest[(major, minor)] = (patch, tag)
    return [
        (f"v{major}.{minor}", tag)
        for (major, minor), (_, tag) in sorted(newest.items(), reverse=True)
    ]


def stage_tree(repo: Path, tag: str, destination: Path) -> None:
    """Write the regular files under the tag's docs/ to destination."""
    listing = git(repo, "ls-tree", "-r", "-z", f"{tag}:{DOCS_DIR}")
    for entry in listing.split(b"\0"):
        if not entry:
            continue
        meta, name = entry.split(b"\t", 1)
        mode, kind, obj = meta.decode().split()
        path = name.decode()
        if kind != "blob" or mode not in REGULAR_FILE_MODES:
            raise ValueError(
                f"{tag}:{DOCS_DIR}/{path}: not a regular file (mode {mode})"
            )
        if any(part in ("", ".", "..") for part in path.split("/")):
            raise ValueError(
                f"{tag}:{DOCS_DIR}/{path}: path leaves the version directory"
            )
        target = destination / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(git(repo, "cat-file", "blob", obj))


def versions_block(lines: list[tuple[str, str]]) -> str:
    default_slug = lines[0][0]
    block = [f"    path: {VERSIONS_DIR}/{default_slug}/{NAV_FILE}", "    versions:"]
    for slug, tag in lines:
        block += [
            f"      - display-name: {tag.removeprefix(TAG_PREFIX)}",
            f"        path: {VERSIONS_DIR}/{slug}/{NAV_FILE}",
            f"        slug: {slug}",
        ]
    return "\n".join(block)


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

    lines = release_lines(args.repo)
    if not lines:
        print("No toolkit release tag has Fern docs; leaving the site unversioned.")
        return 0

    versions_root = args.fern_dir / VERSIONS_DIR
    if versions_root.exists():
        print(
            f"{versions_root} already exists; refusing to overwrite it.",
            file=sys.stderr,
        )
        return 1

    docs_yml = args.fern_dir / "docs.yml"
    config = docs_yml.read_text()
    if config.count(UNVERSIONED_PATH + "\n") != 1:
        print(
            f"{docs_yml}: expected exactly one line {UNVERSIONED_PATH.strip()!r}",
            file=sys.stderr,
        )
        return 1

    for slug, tag in lines:
        stage_tree(args.repo, tag, versions_root / slug)
        print(f"{slug}: {tag}")
    docs_yml.write_text(config.replace(UNVERSIONED_PATH, versions_block(lines)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
