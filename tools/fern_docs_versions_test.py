# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import fern_docs_versions  # noqa: E402


def committed_docs_yml() -> str:
    try:
        from python.runfiles import runfiles

        path = runfiles.Create().Rlocation("_main/fern/docs.yml")
    except ImportError:
        path = None
    if not path:
        path = Path(__file__).resolve().parent.parent / "fern" / "docs.yml"
    return Path(path).read_text()


def versions_section(config: str) -> str:
    """Return the versions list, the last top-level key in docs.yml."""
    return config[config.index("\nversions:\n") + 1 :]


DOCS_YML = committed_docs_yml()
FERN_FILES = {"fern/docs.yml": DOCS_YML, "docs/index.yml": "nav"}


class FernDocsVersionsTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.repo = Path(self._tmp.name) / "repo"
        self.repo.mkdir()
        self.fern = Path(self._tmp.name) / "fern"
        self.fern.mkdir()
        (self.fern / "docs.yml").write_text(DOCS_YML)
        self.git("init", "-q")
        self.git("config", "user.email", "test@example.com")
        self.git("config", "user.name", "Test")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "tag.gpgsign", "false")

    def tearDown(self):
        self._tmp.cleanup()

    def git(self, *args):
        subprocess.run(
            ["git", "-C", str(self.repo), *args], check=True, capture_output=True
        )

    def commit(self, files, *tags):
        for name, text in files.items():
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", "c")
        for tag in tags:
            self.git("tag", tag)

    def run_main(self):
        argv = sys.argv
        sys.argv = [
            "fern_docs_versions",
            "--repo",
            str(self.repo),
            "--fern-dir",
            str(self.fern),
        ]
        try:
            return fern_docs_versions.main()
        finally:
            sys.argv = argv

    def docs_yml(self):
        return (self.fern / "docs.yml").read_text()

    def test_no_fern_bearing_tag_leaves_docs_yml_unchanged(self):
        self.commit({"docs/index.yml": "nav"}, "v0.1.0")
        self.commit({"fern/docs.yml": DOCS_YML}, "v0.2.0-rc.1", "toolkit/v0.2.0")

        self.assertEqual(self.run_main(), 0)
        self.assertEqual(self.docs_yml(), DOCS_YML)
        self.assertEqual(
            versions_section(self.docs_yml()),
            """\
versions:
  - display-name: Latest
    path: ../docs/index.yml
    slug: latest
""",
        )

    def test_one_ref_per_line_newest_first(self):
        self.commit({"README.md": "x"}, "v0.3.0")
        self.commit(FERN_FILES, "v0.4.0")
        self.commit({}, "v0.4.1", "v0.5.0-rc.1", "v0.4")
        self.commit({}, "v0.10.0")

        self.assertEqual(self.run_main(), 0)
        config = self.docs_yml()
        self.assertEqual(
            versions_section(config),
            """\
versions:
  - display-name: v0.10.0
    ref: v0.10.0
    slug: v0.10
  - display-name: v0.4.1
    ref: v0.4.1
    slug: v0.4
""",
        )
        # Everything before the versions list is kept.
        self.assertEqual(
            config[: config.index("\nversions:\n")],
            DOCS_YML[: DOCS_YML.index("\nversions:\n")],
        )

    def test_rejects_unexpected_docs_yml(self):
        (self.fern / "docs.yml").write_text("products:\n")
        self.commit(FERN_FILES, "v1.0.0")

        self.assertEqual(self.run_main(), 1)
        self.assertEqual(self.docs_yml(), "products:\n")


if __name__ == "__main__":
    unittest.main()
