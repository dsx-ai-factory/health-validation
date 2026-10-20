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

DOCS_YML = """\
products:
  - display-name: Health Validation
    path: ../docs/index.yml
    slug: /
"""


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

    def test_no_docs_bearing_tag_leaves_site_unversioned(self):
        self.commit({"README.md": "x"}, "toolkit/v0.1.0")
        self.commit({"docs/index.yml": "nav"}, "catalog/v0.2.0", "toolkit/v0.2.0-rc1")

        self.assertEqual(self.run_main(), 0)
        self.assertEqual((self.fern / "docs.yml").read_text(), DOCS_YML)
        self.assertFalse((self.fern / "versions").exists())

    def test_one_version_per_line_newest_first(self):
        self.commit({"README.md": "x"}, "toolkit/v0.3.0")
        self.commit(
            {"docs/index.yml": "nav 0.4.0", "docs/a/b.md": "old"}, "toolkit/v0.4.0"
        )
        self.commit(
            {"docs/index.yml": "nav 0.4.1", "docs/a/b.md": "patched"}, "toolkit/v0.4.1"
        )
        self.commit({"docs/index.yml": "nav 0.5.0-rc1"}, "toolkit/v0.5.0-rc1")
        self.commit(
            {"docs/index.yml": "nav 0.10.0"}, "toolkit/v0.10.0", "catalog/v0.11.0"
        )

        self.assertEqual(self.run_main(), 0)

        versions = self.fern / "versions"
        self.assertEqual(sorted(p.name for p in versions.iterdir()), ["v0.10", "v0.4"])
        self.assertEqual((versions / "v0.4" / "index.yml").read_text(), "nav 0.4.1")
        self.assertEqual((versions / "v0.4" / "a" / "b.md").read_text(), "patched")
        self.assertEqual((versions / "v0.10" / "index.yml").read_text(), "nav 0.10.0")
        self.assertEqual(
            (self.fern / "docs.yml").read_text(),
            """\
products:
  - display-name: Health Validation
    path: versions/v0.10/index.yml
    versions:
      - display-name: v0.10.0
        path: versions/v0.10/index.yml
        slug: v0.10
      - display-name: v0.4.1
        path: versions/v0.4/index.yml
        slug: v0.4
    slug: /
""",
        )

    def test_rejects_symlink(self):
        self.commit({"docs/index.yml": "nav"})
        os.symlink("/etc/passwd", self.repo / "docs" / "leak.md")
        self.commit({}, "toolkit/v1.0.0")

        with self.assertRaises(ValueError):
            self.run_main()

    def test_refuses_existing_versions_dir(self):
        self.commit({"docs/index.yml": "nav"}, "toolkit/v1.0.0")
        (self.fern / "versions").mkdir()

        self.assertEqual(self.run_main(), 1)
        self.assertEqual((self.fern / "docs.yml").read_text(), DOCS_YML)


if __name__ == "__main__":
    unittest.main()
