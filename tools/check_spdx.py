# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Fail if a hand-written source file lacks an SPDX-License-Identifier header.

Covered: Go, proto, Starlark (.bzl, MODULE.bazel, *.MODULE.bazel), Python, shell,
and JS/TS/CSS. Not covered: generated code, gazelle-managed BUILD.bazel files,
config/YAML, lockfiles, JSON, and docs.

Run as a Bazel test it inspects the real working tree (no-sandbox), found by
resolving the MODULE.bazel symlink in runfiles. Untracked-but-not-ignored files
are included, so a new file is caught before it is ever committed.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

from python.runfiles import runfiles

IDENTIFIER = "SPDX-License-Identifier:"
# The header must appear before any real code; this bounds the search to the
# file's leading comment block (shebang, build tags, and the header itself).
HEAD_BYTES = 1024

SUFFIXES = (
    ".go",
    ".proto",
    ".js",
    ".ts",
    ".tsx",
    ".css",
    ".bzl",
    ".py",
    ".sh",
    ".MODULE.bazel",
)
NAMES = ("MODULE.bazel",)
SKIP_DIRS = ("node_modules/",)


def covered(path: str) -> bool:
    if path.endswith(".pb.go") or any(d in path for d in SKIP_DIRS):
        return False
    return path.endswith(SUFFIXES) or os.path.basename(path) in NAMES


def is_generated(head: str) -> bool:
    return any(line.startswith("// Code generated") for line in head.splitlines())


def workspace_root() -> Path:
    r = runfiles.Create()
    module = r.Rlocation("_main/MODULE.bazel")
    if not module:
        sys.exit("check_spdx: cannot locate MODULE.bazel in runfiles")
    return Path(os.path.realpath(module)).parent


def candidate_files(root: Path) -> list[str]:
    out = subprocess.check_output(
        [
            "git",
            "-C",
            str(root),
            "ls-files",
            "--cached",
            "--others",
            "--exclude-standard",
        ],
        text=True,
    )
    return [p for p in out.splitlines() if covered(p)]


def main() -> int:
    root = workspace_root()
    missing = []
    for rel in candidate_files(root):
        path = root / rel
        if not path.is_file():
            continue
        head = path.read_bytes()[:HEAD_BYTES].decode("utf-8", errors="replace")
        if IDENTIFIER in head or is_generated(head):
            continue
        missing.append(rel)
    if missing:
        print(
            f"{len(missing)} file(s) missing an {IDENTIFIER} header:", file=sys.stderr
        )
        for rel in sorted(missing):
            print(f"  {rel}", file=sys.stderr)
        print(
            "\nAdd, as the first comment (after any shebang):\n"
            "  SPDX-FileCopyrightText: Copyright (c) <year>, NVIDIA CORPORATION & AFFILIATES. All rights reserved.\n"
            "  SPDX-License-Identifier: Apache-2.0",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
