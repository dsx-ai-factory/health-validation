#!/usr/bin/env python3

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Verify — or with --write, regenerate — NOTICE's "Go module dependencies" section.

Two things are checked:

1. Presence, version, and no extras. Every module actually reachable from this
   repo's packages has a NOTICE entry at its currently-resolved version, and
   NOTICE lists nothing beyond that set. "Reachable" means imported (directly or
   transitively, including test-only imports) by a package in the module,
   pulled in by a go.mod `tool` directive, or reached through a
   `//go:build tools` file -- catalog/tools.go blank-imports staticcheck that
   way, so without `-tags tools` its whole dependency tree is invisible to
   `go list`.

   This is deliberately narrower than `go list -m all`, which reports the full
   MVS module-requirement graph including modules nothing here ever imports.
   A module's presence in go.sum is likewise NOT evidence that it ships: go.sum
   records a hash for every module Go had to fetch while resolving versions,
   most of which are never compiled into anything.

2. License accuracy. syft (https://github.com/anchore/syft) reads each module's
   license files out of the local module cache, and NOTICE's License: value must
   match what it found, in both directions.

   Treat any single classifier as a starting point, not an answer. syft
   over-reports, fuzzy-matching a second license against a module's one LICENSE
   file (see SYFT_FALSE_POSITIVES), and other classifiers under-report. A
   disagreement means reading the module's actual license files under
   `go env GOMODCACHE` and deciding by hand; see CONTRIBUTING.md's "Updating
   NOTICE" section.

syft is never used for versions. It reads go.mod require lines literally, with
no MVS, so it can report an older cel.dev/expr than the one the module
actually builds. Versions come from `go list`.
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

from python.runfiles import runfiles

GO_MODULE_DIRS = (".",)
FIRST_PARTY_PREFIX = "github.com/dsx-ai-factory/health-validation"

# Matches what the released container ships, so Linux/cgo-gated files
# (toolkit/nvml, collect/source/dcgm) aren't silently skipped by a host build.
GO_TARGET_ENV = {"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "1"}

SECTION_HEADER = "Go module dependencies:"
SECTION_TERMINATOR = "---"

# Licenses syft reports that a human has checked against the module's actual
# license files and rejected. Each is a classifier false positive, not a license
# we are choosing to omit.
SYFT_FALSE_POSITIVES = {
    # cel-go ships exactly one license file (LICENSE, Apache-2.0). syft
    # concludes both Apache-2.0 and BSD-3-Clause from that same file.
    ("github.com/google/cel-go", "BSD-3-Clause"),
    # The OpenTelemetry modules hit the same classifier quirk: each ships one
    # LICENSE (Apache-2.0) with SPDX-License-Identifier: Apache-2.0 on every
    # source file, and syft concludes BSD-3-Clause alongside it. otel/metric and
    # otel/trace contain no occurrence of the string "BSD" at all, which rules
    # out a stray vendored file and leaves the LICENSE text itself as the source.
    ("go.opentelemetry.io/otel", "BSD-3-Clause"),
    (
        "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp",
        "BSD-3-Clause",
    ),
    ("go.opentelemetry.io/otel/log", "BSD-3-Clause"),
    ("go.opentelemetry.io/otel/metric", "BSD-3-Clause"),
    ("go.opentelemetry.io/otel/sdk", "BSD-3-Clause"),
    ("go.opentelemetry.io/otel/sdk/metric", "BSD-3-Clause"),
    ("go.opentelemetry.io/otel/trace", "BSD-3-Clause"),
}

# syft emits LicenseRef-<sha256> for a license file it could not identify. The
# term carries no information, so it is dropped rather than compared.
LICENSE_REF = re.compile(r"^LicenseRef-[0-9a-f]+$")


def repo_root() -> Path:
    """The source tree, which `bazel run` reports separately from its own cwd."""
    return Path(os.environ["BUILD_WORKSPACE_DIRECTORY"])


def tool_from_runfiles(env_var: str) -> Path:
    """Resolve a binary Bazel exported as an $(rlocationpath) in `env`.

    Every tool this script drives is pinned by Bazel -- the Go SDK by
    rules_go, syft by multitool.lock.json -- so a local run and CI produce
    identical output. That only holds when the binaries come from the
    runfiles, which is why there is no PATH fallback: silently using a
    developer's own build would defeat the pinning.
    """
    rlocation = os.environ.get(env_var)
    resolved = runfiles.Create().Rlocation(rlocation) if rlocation else None
    if not resolved or not Path(resolved).exists():
        sys.exit(
            f"{env_var} did not resolve to a binary. Run this through Bazel:\n"
            "  bazel run //tools:check_notice"
        )
    return Path(resolved)


def go_toolchain(root: Path) -> tuple[Path, dict[str, str]]:
    """Bazel's pinned Go SDK, and the environment it needs to run.

    Go resolves the module versions written into NOTICE, so which Go runs
    matters more than for the other tools: two versions can resolve the same
    go.mod graph differently. The SDK keeps the binary at <root>/bin/go, and
    everything else is found relative to GOROOT.

    The remaining settings pin Go's *behaviour* rather than its binary. Left
    inherited, each one changes what gets resolved: a stray go.work swaps the
    module graph, and GOTOOLCHAIN makes the pinned binary hand off to a
    different toolchain.
    """
    go = tool_from_runfiles("CHECK_NOTICE_GO")
    return go, {
        "GOROOT": str(go.parent.parent),
        # Run this SDK, never download or hand off to another.
        "GOTOOLCHAIN": "local",
        # Ignore whatever `go env -w` has persisted for the developer.
        "GOENV": "off",
        # Drop inherited -mod/-tags, which alter resolution and reachability.
        "GOFLAGS": "",
        # Never pick up a go.work from an enclosing directory.
        "GOWORK": "off",
    }


def run(cmd: list[str], cwd: Path, extra_env: dict[str, str] | None = None) -> str:
    env = {**os.environ, **(extra_env or {})}
    result = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True)
    if result.returncode != 0:
        # stderr is captured, so surface it rather than letting a traceback
        # report only the exit status of a command whose own message explains it.
        sys.exit(
            f"{' '.join(cmd)}\nfailed in {cwd} with exit "
            f"{result.returncode}:\n{result.stderr.strip()}"
        )
    return result.stdout


def tool_directive_targets(module_dir: Path) -> list[str]:
    """Packages named by go.mod `tool` directives.

    A tool stanza's binary is not imported by any package here, so plain
    package-import scanning misses its whole dependency tree.
    """
    targets = []
    for line in (module_dir / "go.mod").read_text().splitlines():
        if line.startswith("tool "):
            targets.append(line.split()[1])
    return targets


def reachable_modules(root: Path, go: Path, go_env: dict[str, str]) -> dict[str, str]:
    """Modules actually compiled, mapped to the version `go list` resolves."""
    modules: dict[str, str] = {}
    for name in GO_MODULE_DIRS:
        module_dir = root / name
        targets = ["./...", *tool_directive_targets(module_dir)]
        stdout = run(
            [
                str(go),
                "list",
                "-deps",
                "-test",
                "-tags",
                "tools",
                "-f",
                "{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}",
                *targets,
            ],
            cwd=module_dir,
            extra_env={**GO_TARGET_ENV, **go_env},
        )
        for line in stdout.splitlines():
            parts = line.split()
            if len(parts) != 2:
                continue
            path, version = parts
            if path.startswith(FIRST_PARTY_PREFIX):
                continue
            modules[path] = version
    return modules


def syft_licenses(root: Path, gomodcache: str, syft: Path) -> dict[str, set[str]]:
    """Module path to the licenses syft concluded from its license files."""
    licenses: dict[str, set[str]] = {}
    for name in GO_MODULE_DIRS:
        stdout = run(
            [str(syft), "scan", f"dir:{name}", "-o", "spdx-tag-value", "-q"],
            cwd=root,
            extra_env={
                "SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES": "true",
                # syft defaults to $GOPATH/pkg/mod, which is not necessarily
                # where the toolchain resolving versions keeps its cache.
                "SYFT_GOLANG_LOCAL_MOD_CACHE_DIR": gomodcache,
            },
        )
        package = None
        for line in stdout.splitlines():
            if line.startswith("PackageName: "):
                package = line.removeprefix("PackageName: ").strip()
            elif line.startswith("PackageLicenseConcluded: ") and package:
                expression = line.removeprefix("PackageLicenseConcluded: ").strip()
                if expression == "NOASSERTION":
                    continue
                found = {
                    term
                    for term in expression.split(" AND ")
                    if not LICENSE_REF.match(term)
                }
                if found:
                    licenses.setdefault(package, set()).update(found)
    return licenses


def parse_notice(text: str) -> tuple[dict[str, str], dict[str, str]]:
    """Extract the Go section's versions and License: values, keyed by module."""
    versions: dict[str, str] = {}
    declared: dict[str, str] = {}
    in_section = False
    module = None

    for line in text.splitlines():
        if line == SECTION_HEADER:
            in_section = True
            continue
        if line == SECTION_TERMINATOR:
            in_section = False
            continue
        if not in_section or not line.strip():
            continue

        entry = line.strip()
        if entry.startswith("License:"):
            if module:
                declared[module] = entry.removeprefix("License:").strip()
        elif not entry.startswith("URL:"):
            parts = entry.split()
            if len(parts) == 2:
                module = parts[0]
                versions[module] = parts[1]

    return versions, declared


def render_section(
    modules: dict[str, str],
    declared: dict[str, str],
    found: dict[str, set[str]],
) -> str:
    """Build a replacement Go section, preserving reviewed License: values."""
    lines = [SECTION_HEADER]
    for path in sorted(modules):
        # A classifier is not authoritative enough to overwrite a human
        # decision, so an existing License: value is carried forward untouched.
        # syft only fills in modules new to NOTICE, which the check then
        # surfaces for review.
        license_value = declared.get(path)
        if not license_value:
            license_value = " AND ".join(sorted(found.get(path, ()))) or (
                "UNKNOWN — read the module's license files and fill this in"
            )
        lines.append("")
        lines.append(f"  {path}  {modules[path]}")
        lines.append(f"  License:  {license_value}")
        lines.append(f"  URL:      https://{path}")
    lines.append("")
    return "\n".join(lines) + "\n"


def splice_section(notice: str, section: str) -> str:
    """Replace the Go section in NOTICE, leaving every other section intact."""
    out: list[str] = []
    skipping = False
    replaced = False

    for line in notice.splitlines(keepends=True):
        if not replaced and line.rstrip("\n") == SECTION_HEADER:
            out.append(section)
            skipping = True
            replaced = True
            continue
        if skipping:
            if line.rstrip("\n") == SECTION_TERMINATOR:
                skipping = False
                out.append(line)
            continue
        out.append(line)

    return "".join(out)


def check(
    modules: dict[str, str],
    versions: dict[str, str],
    declared: dict[str, str],
    found: dict[str, set[str]],
) -> list[str]:
    problems: list[str] = []

    if missing := sorted(set(modules) - set(versions)):
        problems.append(
            "NOTICE is missing an entry for:\n" + "\n".join(f"  {m}" for m in missing)
        )

    if extra := sorted(set(versions) - set(modules)):
        problems.append(
            "NOTICE lists modules this repo does not compile (being in go.sum is "
            "not enough):\n" + "\n".join(f"  {m}" for m in extra)
        )

    for path, version in sorted(modules.items()):
        recorded = versions.get(path)
        if recorded and recorded != version:
            problems.append(
                f"NOTICE has a stale version for {path}: "
                f"NOTICE has {recorded}, resolved to {version}"
            )

    unverified = []
    for path in sorted(modules):
        reported = found.get(path)
        if not reported:
            unverified.append(path)
            continue
        claimed = declared.get(path)
        if claimed is None:
            # An entry whose module line is present but whose License: line is
            # not never reaches the missing-entry check above, which compares
            # module paths only. Left to fall through it would be silently
            # exempt from license checking.
            if path in versions:
                problems.append(f"NOTICE's entry for {path} has no License: value")
            continue
        claimed_terms = set(claimed.split(" AND "))

        for license_id in sorted(reported - claimed_terms):
            if (path, license_id) in SYFT_FALSE_POSITIVES:
                continue
            problems.append(
                f"syft found {license_id} for {path}, which NOTICE's "
                f"License: value ({claimed}) omits"
            )
        for license_id in sorted(claimed_terms - reported):
            problems.append(
                f"NOTICE claims {license_id} for {path}, which syft did not "
                f"find ({' AND '.join(sorted(reported))})"
            )

    if unverified:
        print(
            "Note: syft could not classify these modules — their License: "
            "values were not cross-checked and must be verified by hand if "
            "changed:",
            file=sys.stderr,
        )
        for path in unverified:
            print(f"  {path}", file=sys.stderr)

    return problems


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--write",
        action="store_true",
        help="regenerate NOTICE's Go section instead of checking it",
    )
    args = parser.parse_args()

    root = repo_root()
    notice_path = root / "NOTICE"

    syft = tool_from_runfiles("CHECK_NOTICE_SYFT")
    go, go_env = go_toolchain(root)

    gomodcache = run([str(go), "env", "GOMODCACHE"], cwd=root, extra_env=go_env).strip()
    modules = reachable_modules(root, go, go_env)
    found = syft_licenses(root, gomodcache, syft)

    # A license checker that classifies nothing reports no problems, which reads
    # exactly like success. Fail loudly instead — this script's predecessor spent
    # its whole life in that state.
    if not found:
        print(
            f"syft classified no licenses at all (module cache: {gomodcache}).\n"
            "Every License: value would go unchecked, so this is a hard failure.",
            file=sys.stderr,
        )
        return 1

    notice = notice_path.read_text()
    versions, declared = parse_notice(notice)

    if args.write:
        updated = splice_section(notice, render_section(modules, declared, found))
        if updated == notice:
            print("NOTICE already up to date — no changes written.")
            return 0
        notice_path.write_text(updated)
        print(f"NOTICE rewritten ({len(modules)} third-party module(s)).")
        print(
            "Review the diff: any License: value shown as UNKNOWN needs a human.",
            file=sys.stderr,
        )
        return 0

    problems = check(modules, versions, declared, found)
    if problems:
        for problem in problems:
            print(problem, file=sys.stderr)
        print(
            "\nRun 'bazel run //tools:check_notice -- --write' to regenerate, "
            "then review the diff.\nSee CONTRIBUTING.md's 'Updating NOTICE' "
            "section.",
            file=sys.stderr,
        )
        return 1

    checked = sum(1 for path in modules if found.get(path))
    print(
        f"NOTICE is up to date ({len(modules)} third-party module(s), "
        f"{checked} license(s) cross-checked)."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
