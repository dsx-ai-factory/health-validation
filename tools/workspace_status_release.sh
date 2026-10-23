#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

#
# Release and pre-release workspace status for health-validation.
#
# Emits the STABLE_ values consumed by the x_defs on
# //toolkit/cmd/health-validation:health-validation. Bazel invokes this through
# --config=release, which also enables stamping. Plain builds remain unstamped
# and report the package defaults.
#
# Version rules:
#   * HEALTH_VALIDATION_VERSION, when set, is stamped as given. CI's
#     release-metadata job resolves it once and every later stamped build in the
#     run reuses it.
#   * A GitHub tag event named vX.Y.Z (or vX.Y.Z-rcN) stamps that semantic
#     version into the operator.
#   * Every other build gets a Go pseudo-version derived from the most recent
#     release, the commit time, and the 12-character commit SHA. A final
#     release of the same MAJOR.MINOR line tagged on a release branch counts as
#     most recent even though HEAD cannot reach it.
#   * The repository is one Go module, so one version covers the operator and
#     the catalog compiled into it.
#   * Unstamped local builds retain the packages' development identifiers.
#
# The pseudo-version is deterministic for a commit and a set of tags, sorts
# after its base release and before the next release, and identifies the exact
# main-branch build. Tagging a final release on a release branch moves main's
# later builds from vX.Y.Z-rcN.0.* to vX.Y.(Z+1)-0.*. Image
# publishing removes its leading v and also applies latest on main.

set -euo pipefail

commit="$(git rev-parse HEAD)"
short="$(git rev-parse --short=12 HEAD)"

# Use the commit time rather than the wall clock so rebuilding the same commit
# produces the same version. format-local honors TZ=UTC; --date=format does not
# and can incorrectly label a local wall-clock value with a trailing Z.
pseudo_ts="$(TZ=UTC git show -s --date=format-local:%Y%m%d%H%M%S --format=%cd HEAD)"
build_date="$(TZ=UTC git show -s --date=format-local:%Y-%m-%dT%H:%M:%SZ --format=%cd HEAD)"

# Prints the patch number of the newest final release in a MAJOR.MINOR line that
# HEAD does not predate. Finals are tagged on release/vX.Y after QA,
# so main never reaches them; without this, main would stay a prerelease of the
# RC it branched from and sort below a version that has already shipped.
latest_line_release() {
	local line="$1" tag

	while IFS= read -r tag; do
		[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue
		# A release built on top of HEAD is in HEAD's future, not its past.
		git merge-base --is-ancestor HEAD "${tag}^{commit}" && continue
		printf '%s\n' "${tag##*.}"
		return
	done < <(git tag --list "v${line}.*" --sort=-v:refname)
}

pseudo_version() {
	local base major minor patch released rc=0

	base="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)"
	base="${base:-v0.0.0}"
	base="${base#v}"

	IFS=. read -r major minor patch <<<"$base"
	if [[ "$patch" =~ ^[0-9]+-rc[0-9]+$ ]]; then
		rc=1
	fi
	patch="${patch%%[-+]*}"

	released="$(latest_line_release "${major}.${minor}")"
	if [[ -n "$released" ]] &&
		((10#$released > 10#$patch || (10#$released == 10#$patch && rc))); then
		patch="$released"
		rc=0
	fi

	# Commits after an RC remain prereleases of the same final version.
	if ((rc)); then
		printf 'v%s.0.%s-%s\n' "$base" "$pseudo_ts" "$short"
		return
	fi
	printf 'v%s.%s.%s-0.%s-%s\n' "$major" "$minor" "$((10#$patch + 1))" "$pseudo_ts" "$short"
}

semver_re='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

if [[ -n "${HEALTH_VALIDATION_VERSION:-}" ]]; then
	# CI resolves the version once and passes it to every later build.
	# Resolving again per job would let a tag pushed mid-run stamp a binary with
	# a version other than the one its image is published under.
	version="$HEALTH_VALIDATION_VERSION"
	if [[ ! "$version" =~ $semver_re ]]; then
		echo "workspace_status_release: HEALTH_VALIDATION_VERSION must be a vX.Y.Z version; got '$version'" >&2
		exit 1
	fi
else
	event_tag=""
	if [[ "${GITHUB_REF_TYPE:-}" == "tag" ]]; then
		event_tag="${GITHUB_REF_NAME:-}"
	fi

	if [[ "$event_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]]; then
		version="$event_tag"
	elif [[ -z "$event_tag" ]]; then
		# Main builds advance the patch component of the most recent release.
		# A repository with no release starts from v0.0.0.
		version="$(pseudo_version)"
	else
		echo "workspace_status_release: expected vX.Y.Z, optionally followed by -rcN; got '$event_tag'" >&2
		exit 1
	fi
fi

echo "STABLE_HEALTH_VALIDATION_VERSION ${version}"
echo "STABLE_GIT_COMMIT ${commit}"
echo "STABLE_BUILD_DATE ${build_date}"
