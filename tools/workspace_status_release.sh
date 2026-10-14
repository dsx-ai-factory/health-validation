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
#   * HEALTH_VALIDATION_TOOLKIT_VERSION and HEALTH_VALIDATION_CATALOG_VERSION,
#     when set, are stamped as given. CI's release-metadata job resolves both
#     once and every later stamped build in the run reuses them.
#   * A GitHub tag event named toolkit/vX.Y.Z (or toolkit/vX.Y.Z-rcN) stamps
#     that semantic version into the operator.
#   * Every other build gets a Go pseudo-version derived from the most recent
#     toolkit release, the commit time, and the 12-character commit SHA. A
#     final release of the same MAJOR.MINOR line tagged on a release branch
#     counts as most recent even though HEAD cannot reach it.
#   * Catalog sources matching their latest release keep that semantic version;
#     changed sources get a pseudo-version, including in toolkit releases.
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

# Prints the patch number of the newest final release in a module's MAJOR.MINOR
# line that HEAD does not predate. Finals are tagged on release/vX.Y after QA,
# so main never reaches them; without this, main would stay a prerelease of the
# RC it branched from and sort below a version that has already shipped.
latest_line_release() {
	local module="$1" line="$2" tag

	while IFS= read -r tag; do
		[[ "$tag" =~ ^${module}/v[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue
		# A release built on top of HEAD is in HEAD's future, not its past.
		git merge-base --is-ancestor HEAD "${tag}^{commit}" && continue
		printf '%s\n' "${tag##*.}"
		return
	done < <(git tag --list "${module}/v${line}.*" --sort=-v:refname)
}

pseudo_version() {
	local module="$1"
	local base major minor patch released rc=0

	base="$(git describe --tags --abbrev=0 --match "${module}/v[0-9]*.[0-9]*.[0-9]*" 2>/dev/null || true)"
	base="${base:-${module}/v0.0.0}"
	base="${base#"${module}/v"}"

	IFS=. read -r major minor patch <<<"$base"
	if [[ "$patch" =~ ^[0-9]+-rc[0-9]+$ ]]; then
		rc=1
	fi
	patch="${patch%%[-+]*}"

	released="$(latest_line_release "$module" "${major}.${minor}")"
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

if [[ -n "${HEALTH_VALIDATION_TOOLKIT_VERSION:-}${HEALTH_VALIDATION_CATALOG_VERSION:-}" ]]; then
	# CI resolves versions once and passes them to every later build. Resolving
	# again per job would let a tag pushed mid-run stamp a binary with a version
	# other than the one its image is published under.
	toolkit_version="${HEALTH_VALIDATION_TOOLKIT_VERSION:-}"
	catalog_version="${HEALTH_VALIDATION_CATALOG_VERSION:-}"
	if [[ ! "$toolkit_version" =~ $semver_re || ! "$catalog_version" =~ $semver_re ]]; then
		echo "workspace_status_release: HEALTH_VALIDATION_TOOLKIT_VERSION and HEALTH_VALIDATION_CATALOG_VERSION must both be set to vX.Y.Z versions; got '$toolkit_version' and '$catalog_version'" >&2
		exit 1
	fi
else
	event_tag=""
	if [[ "${GITHUB_REF_TYPE:-}" == "tag" ]]; then
		event_tag="${GITHUB_REF_NAME:-}"
	fi

	if [[ "$event_tag" =~ ^toolkit/v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]]; then
		# The module prefix belongs to the Git tag, not the embedded version.
		toolkit_version="${event_tag#toolkit/}"
	elif [[ -z "$event_tag" || "$event_tag" =~ ^catalog/v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]]; then
		# Main and catalog-release builds advance the patch component of the most
		# recent toolkit release.
		# A repository with no toolkit release starts from v0.0.0.
		toolkit_version="$(pseudo_version toolkit)"
	else
		echo "workspace_status_release: expected toolkit/vX.Y.Z or catalog/vX.Y.Z, optionally followed by -rcN; got '$event_tag'" >&2
		exit 1
	fi

	# A catalog release remains the embedded version across toolkit-only commits.
	# Once catalog/ changes, stamped builds use a catalog pseudo-version.
	catalog_release_tag="$(git describe --tags --abbrev=0 --match 'catalog/v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)"
	if [[ "$catalog_release_tag" =~ ^catalog/v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$ ]] &&
		git diff --quiet "$catalog_release_tag" HEAD -- catalog; then
		catalog_version="${catalog_release_tag#catalog/}"
	else
		catalog_version="$(pseudo_version catalog)"
	fi
fi

echo "STABLE_HEALTH_VALIDATION_VERSION ${toolkit_version}"
echo "STABLE_HEALTH_VALIDATION_CATALOG_VERSION ${catalog_version}"
echo "STABLE_GIT_COMMIT ${commit}"
echo "STABLE_BUILD_DATE ${build_date}"
