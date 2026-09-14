#!/usr/bin/env bash
#
# Verifies — or with --write, regenerates — NOTICE's "Go module
# dependencies" section.
#
#   1. Presence + version: every module actually reachable from this
#      repo's packages has a NOTICE entry at its current version, and
#      NOTICE lists nothing beyond that set.
#      "Reachable" means imported (directly or transitively, including
#      test-only imports) by a package in any of the four go.work
#      modules (agentkit, catalog, collect, toolkit), pulled in by one
#      of their go.mod `tool` directives, or reached through a
#      `//go:build tools` file (catalog/tools.go blank-imports
#      staticcheck that way; without -tags tools its whole dependency
#      tree is invisible to `go list`). This is deliberately narrower
#      than `go list -m all`, which reports the full MVS module-
#      requirement graph — including modules nothing here ever imports,
#      needed only to resolve some other dependency's own go.mod.
#
#      A module's presence in go.sum is NOT evidence it ships: go.sum
#      records a hash for every go.mod Go had to read during version
#      resolution, most of which are never compiled.
#
#   2. License accuracy: syft (https://github.com/anchore/syft) reads
#      each module's license files out of the local module cache and
#      NOTICE's License: value must match what it found.
#
#      Treat a single classifier's output as a starting point, not an
#      answer — syft over-reports (it fuzzy-matches a second license
#      against a module's one LICENSE file; see syft_false_positive)
#      and other classifiers under-report. A disagreement means read
#      the module's actual license files in
#      $(go env GOMODCACHE)/<module>@<version>/ and decide by hand; see
#      CONTRIBUTING.md's "Updating NOTICE" section.
#
# GOOS=linux GOARCH=amd64 CGO_ENABLED=1 matches what the released
# container actually ships, so Linux/cgo-gated files (toolkit/nvml,
# collect/source/dcgm) aren't silently skipped by a host build.
#
# Usage:
#   tools/check_notice.sh              # uses "go" on PATH
#   tools/check_notice.sh --write      # rewrite NOTICE's Go section
#   BAZEL_GO=1 tools/check_notice.sh   # uses Bazel's pinned Go SDK (CI)
set -euo pipefail

# This script compares sorted lists with sort(1) and comm(1), which must
# agree on collation order. Pin it for every one of them at once.
export LC_ALL=C

cd "$(dirname "${BASH_SOURCE[0]}")/.."

GO_MODULE_DIRS=(agentkit catalog collect toolkit)
FIRST_PARTY_PREFIX=github.com/dsx-ai-factory/health-validation

write=0
case "${1:-}" in
--write) write=1 ;;
"") ;;
*)
	echo "usage: ${BASH_SOURCE[0]##*/} [--write]" >&2
	exit 2
	;;
esac

# Licenses syft reports that a human has checked against the module's
# actual license files and rejected. Each entry is a classifier false
# positive, not a license we're choosing to omit.
#
#   cel-go ships exactly one license file (LICENSE, Apache-2.0). syft
#   concludes both Apache-2.0 and BSD-3-Clause from that same file.
syft_false_positive() {
	case "$1 $2" in
	"github.com/google/cel-go BSD-3-Clause") return 0 ;;
	*) return 1 ;;
	esac
}

run_go() {
	if [[ "${BAZEL_GO:-}" == "1" ]]; then
		bazel run //tools/go -- "$@"
	else
		go "$@"
	fi
}

if ! command -v syft >/dev/null 2>&1; then
	echo "syft not found on PATH — it classifies the licenses NOTICE reports." >&2
	echo "Install: https://github.com/anchore/syft#installation" >&2
	exit 1
fi

# syft defaults to $GOPATH/pkg/mod, which is not necessarily where the
# toolchain running this check ("go" on PATH, or Bazel's pinned SDK under
# BAZEL_GO=1) keeps its cache. Point syft at the same cache the module
# list comes from, so both halves see identical inputs.
gomodcache="$(run_go env GOMODCACHE)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

status=0

: >"$tmp/used_raw.txt"
: >"$tmp/syft_raw.txt"

for mod_dir in "${GO_MODULE_DIRS[@]}"; do
	targets=("./...")
	while read -r tool_pkg; do
		[[ -n "$tool_pkg" ]] && targets+=("$tool_pkg")
	done < <(awk '/^tool /{print $2}' "$mod_dir/go.mod")

	(
		cd "$mod_dir" &&
			GOOS=linux GOARCH=amd64 CGO_ENABLED=1 run_go list -deps -test -tags tools \
				-f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' "${targets[@]}"
	) >>"$tmp/used_raw.txt"

	# syft reads licenses straight out of the module cache. Its *versions*
	# are the literal go.mod require lines — pre-MVS, and blind to go.work
	# — so they are deliberately discarded here; versions come from
	# `go list` above, which reports what actually builds.
	SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true \
		SYFT_GOLANG_LOCAL_MOD_CACHE_DIR="$gomodcache" \
		syft scan "dir:$mod_dir" -o spdx-tag-value -q >>"$tmp/syft_raw.txt"
done

grep -v "^$FIRST_PARTY_PREFIX" "$tmp/used_raw.txt" | grep -v '^[[:space:]]*$' |
	sort -u -k1,1 >"$tmp/used.txt"

# module <TAB> license-expression, for every module syft could classify.
# LicenseRef-<sha256> means "a license file syft could not identify" —
# it carries no information, so drop those terms (and any module left
# with nothing but them).
awk '
  /^PackageName: / { name = substr($0, 14); next }
  /^PackageLicenseConcluded: / {
    lic = substr($0, 26)
    if (name != "" && lic != "NOASSERTION") print name "\t" lic
    next
  }
' "$tmp/syft_raw.txt" |
	sed -e 's/LicenseRef-[0-9a-f]* AND //g' \
		-e 's/ AND LicenseRef-[0-9a-f]*//g' \
		-e '/\tLicenseRef-[0-9a-f]*$/d' |
	sort -u >"$tmp/syft_licenses.txt"

# A license checker that classifies nothing reports no problems, which
# reads exactly like success. Fail loudly instead — the predecessor of
# this script spent its whole life in that state.
if [[ ! -s "$tmp/syft_licenses.txt" ]]; then
	echo "syft classified no licenses at all (module cache: $gomodcache)." >&2
	echo "Every License: value would go unchecked, so this is a hard failure." >&2
	exit 1
fi

#### Parse the current NOTICE. ####

# NOTICE's "  <module>  <version>" lines, stopping before the "Bundled
# source modifications" section (not Go-module-shaped).
awk '
  /^Go module dependencies:$/ { in_section = 1; next }
  /^---$/ { in_section = 0 }
  in_section && /^  [^ ]/ {
    n = split($0, f, /  +/)
    if (n >= 3 && f[2] != "License:" && f[2] != "URL:") print f[2], f[3]
  }
' NOTICE | sort -u -k1,1 >"$tmp/notice_versions.txt"

# NOTICE's "  License:  <value>" line for each module, keyed by the
# module path from the line above it.
awk '
  /^Go module dependencies:$/ { in_section = 1; next }
  /^---$/ { in_section = 0 }
  in_section && /^  [^ ]/ && $0 !~ /^  License:/ && $0 !~ /^  URL:/ {
    n = split($0, f, /  +/)
    if (n >= 3) mod = f[2]
    next
  }
  in_section && /^  License:  / { print mod "\t" substr($0, 13) }
' NOTICE | sort -u >"$tmp/notice_licenses.txt"

#### --write: regenerate the Go section in place. ####

if [[ "$write" -eq 1 ]]; then
	{
		echo "Go module dependencies:"
		while read -r mod ver; do
			[[ -z "$mod" ]] && continue
			# Carry forward the reviewed License: value when NOTICE already
			# has one. A classifier is not authoritative enough to overwrite
			# a human decision; it only fills in modules new to NOTICE, which
			# the check below then reports for review.
			lic="$(awk -F'\t' -v m="$mod" '$1 == m { print $2; exit }' "$tmp/notice_licenses.txt")"
			if [[ -z "$lic" ]]; then
				lic="$(awk -F'\t' -v m="$mod" '$1 == m { print $2; exit }' "$tmp/syft_licenses.txt")"
				[[ -z "$lic" ]] && lic="UNKNOWN — read the module's license files and fill this in"
			fi
			printf '\n  %s  %s\n' "$mod" "$ver"
			printf '  License:  %s\n' "$lic"
			printf '  URL:      https://%s\n' "$mod"
		done <"$tmp/used.txt"
		echo
	} >"$tmp/go_section.txt"

	awk -v section="$tmp/go_section.txt" '
    /^Go module dependencies:$/ && !done {
      while ((getline line < section) > 0) print line
      close(section)
      skipping = 1
      done = 1
      next
    }
    skipping && /^---$/ { skipping = 0 }
    !skipping { print }
  ' NOTICE >"$tmp/NOTICE.new"

	if cmp -s NOTICE "$tmp/NOTICE.new"; then
		echo "NOTICE already up to date — no changes written."
	else
		cp "$tmp/NOTICE.new" NOTICE
		echo "NOTICE rewritten ($(wc -l <"$tmp/used.txt" | tr -d ' ') third-party module(s))."
		echo "Review the diff: any License: value shown as UNKNOWN needs a human." >&2
	fi
	exit 0
fi

#### Part 1: presence, version, and no extras. ####

missing="$(comm -23 <(cut -d' ' -f1 "$tmp/used.txt") <(cut -d' ' -f1 "$tmp/notice_versions.txt"))"
if [[ -n "$missing" ]]; then
	echo "NOTICE is missing an entry for:" >&2
	echo "$missing" | sed 's/^/  /' >&2
	echo >&2
	status=1
fi

extra="$(comm -13 <(cut -d' ' -f1 "$tmp/used.txt") <(cut -d' ' -f1 "$tmp/notice_versions.txt"))"
if [[ -n "$extra" ]]; then
	echo "NOTICE lists modules this repo does not compile (being in go.sum is not enough):" >&2
	echo "$extra" | sed 's/^/  /' >&2
	echo >&2
	status=1
fi

while read -r mod ver; do
	[[ -z "$mod" ]] && continue
	notice_ver="$(awk -v m="$mod" '$1 == m { print $2 }' "$tmp/notice_versions.txt")"
	if [[ -n "$notice_ver" && "$notice_ver" != "$ver" ]]; then
		echo "NOTICE has a stale version for $mod: NOTICE has $notice_ver, resolved to $ver" >&2
		status=1
	fi
done <"$tmp/used.txt"

#### Part 2: license accuracy. ####

# NOTICE may join multiple licenses with " AND " (e.g. go.yaml.in/yaml/v3,
# whose single LICENSE file explicitly covers its files under both MIT and
# Apache-2.0). Both directions are checked: a license syft found that
# NOTICE omits is under-attribution, and one NOTICE claims that syft did
# not find is either stale or a license removed upstream.
license_in_list() {
	# $1: a single license; $2: an " AND "-joined expression.
	case " AND $2 AND " in
	*" AND $1 AND "*) return 0 ;;
	*) return 1 ;;
	esac
}

unverified=()
checked=0

while read -r mod _ver; do
	[[ -z "$mod" ]] && continue
	syft_expr="$(awk -F'\t' -v m="$mod" '$1 == m { print $2; exit }' "$tmp/syft_licenses.txt")"
	notice_expr="$(awk -F'\t' -v m="$mod" '$1 == m { print $2; exit }' "$tmp/notice_licenses.txt")"

	if [[ -z "$syft_expr" ]]; then
		unverified+=("$mod")
		continue
	fi
	[[ -z "$notice_expr" ]] && continue # already reported as missing above

	checked=$((checked + 1))

	for found in ${syft_expr// AND / }; do
		if ! license_in_list "$found" "$notice_expr"; then
			if syft_false_positive "$mod" "$found"; then
				continue
			fi
			echo "syft found $found for $mod, which NOTICE's License: value ($notice_expr) omits" >&2
			status=1
		fi
	done

	for claimed in ${notice_expr// AND / }; do
		if ! license_in_list "$claimed" "$syft_expr"; then
			echo "NOTICE claims $claimed for $mod, which syft did not find ($syft_expr)" >&2
			status=1
		fi
	done
done <"$tmp/used.txt"

if [[ "${#unverified[@]}" -gt 0 ]]; then
	echo "Note: syft could not classify these modules — their License: values were not cross-checked and must be verified by hand if changed:" >&2
	printf '  %s\n' "${unverified[@]-}" >&2
fi

if [[ "$status" -ne 0 ]]; then
	echo >&2
	echo "Run 'tools/check_notice.sh --write' to regenerate, then review the diff." >&2
	echo "See CONTRIBUTING.md's 'Updating NOTICE' section." >&2
	exit "$status"
fi

echo "NOTICE is up to date ($(wc -l <"$tmp/used.txt" | tr -d ' ') third-party module(s), $checked license(s) cross-checked)."
