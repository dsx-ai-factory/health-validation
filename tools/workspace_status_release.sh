#!/usr/bin/env bash
#
# Release and pre-release workspace status for health-validation.
#
# Emits the STABLE_ values consumed by the x_defs on
# //toolkit/cmd/health-validation:health-validation. Bazel invokes this through
# --config=release, which also enables stamping. Plain builds remain unstamped
# and report the package defaults.
#
# Version rules:
#   * A GitHub tag event named toolkit/vX.Y.Z (or toolkit/vX.Y.Z-rcN) stamps
#     that semantic version into the operator.
#   * Every other build gets a Go pseudo-version derived from the most recent
#     toolkit release, the commit time, and the 12-character commit SHA.
#   * Catalog sources matching their latest release keep that semantic version;
#     changed sources get a pseudo-version, including in toolkit releases.
#   * Unstamped local builds retain the packages' development identifiers.
#
# The pseudo-version is deterministic for a commit, sorts after its base release
# and before the next release, and identifies the exact main-branch build. Image
# publishing removes its leading v and also applies latest on main.

set -euo pipefail

commit="$(git rev-parse HEAD)"
short="$(git rev-parse --short=12 HEAD)"

# Use the commit time rather than the wall clock so rebuilding the same commit
# produces the same version. format-local honors TZ=UTC; --date=format does not
# and can incorrectly label a local wall-clock value with a trailing Z.
pseudo_ts="$(TZ=UTC git show -s --date=format-local:%Y%m%d%H%M%S --format=%cd HEAD)"
build_date="$(TZ=UTC git show -s --date=format-local:%Y-%m-%dT%H:%M:%SZ --format=%cd HEAD)"

pseudo_version() {
	local module="$1"
	local base major minor patch

	base="$(git describe --tags --abbrev=0 --match "${module}/v[0-9]*.[0-9]*.[0-9]*" 2>/dev/null || true)"
	base="${base:-${module}/v0.0.0}"
	base="${base#"${module}/v"}"
	IFS=. read -r major minor patch <<<"$base"
	patch="${patch%%[-+]*}"
	printf 'v%s.%s.%s-0.%s-%s\n' "$major" "$minor" "$((10#$patch + 1))" "$pseudo_ts" "$short"
}

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

echo "STABLE_HEALTH_VALIDATION_VERSION ${toolkit_version}"
echo "STABLE_HEALTH_VALIDATION_CATALOG_VERSION ${catalog_version}"
echo "STABLE_GIT_COMMIT ${commit}"
echo "STABLE_BUILD_DATE ${build_date}"
