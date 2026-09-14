#!/usr/bin/env bash
#
# Verifies that NOTICE's "Go module dependencies" section is up to date:
#
#   1. Presence + version: every module actually reachable from this
#      repo's packages has a NOTICE entry at its current version.
#      "Reachable" means imported (directly or transitively, including
#      test-only imports) by a package in any of the four go.work
#      modules (agentkit, catalog, collect, toolkit), or pulled in by
#      one of their go.mod `tool` directives (a `tool` stanza's binary
#      isn't imported by any package here, so plain package-import
#      scanning misses its whole dependency tree). This is deliberately
#      narrower than `go list -m all`, which reports the full MVS
#      module-requirement graph — including modules nothing here ever
#      actually imports, needed only to resolve some other dependency's
#      own go.mod (e.g. grpc's go.mod requires a large Cloud/OpenTelemetry
#      graph for its own examples/tests, none of which this repo uses).
#
#   2. License accuracy: for every module go-licenses (see
#      https://github.com/google/go-licenses) can classify, NOTICE's
#      License: value is among what go-licenses reports for it. A
#      module go-licenses can't classify (usually the same "not
#      actually reachable" modules Part 1 already excludes) is listed
#      as unverified rather than failed; if it's genuinely new, its
#      License: value still needs a human to check the LICENSE file by
#      hand (see CONTRIBUTING.md's "Updating NOTICE" section).
#
# GOOS=linux GOARCH=amd64 CGO_ENABLED=1 matches what the released
# container actually ships, so Linux/cgo-gated files (toolkit/nvml,
# collect/source/dcgm) aren't silently skipped by a host build.
#
# Usage:
#   tools/check_notice.sh              # uses "go" on PATH
#   BAZEL_GO=1 tools/check_notice.sh   # uses Bazel's pinned Go SDK (CI)
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

GO_LICENSES_VERSION=v2.0.1

run_go() {
  if [[ "${BAZEL_GO:-}" == "1" ]]; then
    bazel run //tools/go -- "$@"
  else
    go "$@"
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

status=0

# go-licenses is built for the host (no GOOS/GOARCH override — those are
# only set below when *invoking* it, to point its package analysis at
# the container's target platform). Built once, ahead of the per-module
# loop: `go run pkg@version` under a GOOS=linux/GOARCH=amd64 environment
# would instead cross-compile go-licenses itself into a binary this host
# can't execute.
run_go install "github.com/google/go-licenses/v2@${GO_LICENSES_VERSION}"
go_licenses_bin="$(run_go env GOPATH)/bin/go-licenses"

: >"$tmp/used.txt"
: >"$tmp/go_licenses_raw.csv"

for mod_dir in agentkit catalog collect toolkit; do
  targets=("./...")
  while read -r tool_pkg; do
    [[ -n "$tool_pkg" ]] && targets+=("$tool_pkg")
  done < <(awk '/^tool /{print $2}' "$mod_dir/go.mod")

  (
    cd "$mod_dir" &&
      GOOS=linux GOARCH=amd64 CGO_ENABLED=1 run_go list -deps -test \
        -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' "${targets[@]}"
  ) >>"$tmp/used.txt"

  (
    cd "$mod_dir" &&
      GOOS=linux GOARCH=amd64 CGO_ENABLED=1 "$go_licenses_bin" \
        report "${targets[@]}" --include_tests 2>/dev/null || true
  ) >>"$tmp/go_licenses_raw.csv"
done

grep -v '^github.com/dsx-ai-factory/health-validation' "$tmp/used.txt" |
  sort -u -k1,1 -o "$tmp/used.txt"

#### Part 1: presence + version. ####

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
  in_section && /^  License:  / { print mod, substr($0, 13) }
' NOTICE >"$tmp/notice_licenses.txt"

missing="$(comm -23 <(cut -d' ' -f1 "$tmp/used.txt") <(cut -d' ' -f1 "$tmp/notice_versions.txt"))"
if [[ -n "$missing" ]]; then
  echo "NOTICE is missing an entry for:" >&2
  echo "$missing" | sed 's/^/  /' >&2
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

# go-licenses reports per *package* import path, not per module (e.g.
# "golang.org/x/sys/unix" for module "golang.org/x/sys"). Reduce each
# row to its owning module by longest-prefix match against the modules
# Part 1 found reachable, then collect the (possibly several — a
# classifier can report more than one plausible match for the same
# LICENSE file, or a module can genuinely bundle sub-components under a
# different license) licenses reported per module.
sort -t, -k1,1 -u "$tmp/go_licenses_raw.csv" >"$tmp/go_licenses.csv"
cut -d' ' -f1 "$tmp/used.txt" | sort -u >"$tmp/known_modules.txt"

: >"$tmp/module_licenses.txt"
while IFS=, read -r pkg _url license; do
  [[ -z "$pkg" || "$license" == "Unknown" ]] && continue
  best=""
  while read -r m; do
    case "$pkg" in
    "$m" | "$m"/*)
      if [[ ${#m} -gt ${#best} ]]; then best="$m"; fi
      ;;
    esac
  done <"$tmp/known_modules.txt"
  [[ -n "$best" ]] && echo "$best $license" >>"$tmp/module_licenses.txt"
done <"$tmp/go_licenses.csv"

unverified=()
# NOTICE may join multiple licenses with " AND " (e.g. a dual-licensed
# module). Checked in this direction only: every license go-licenses
# reported must appear in NOTICE. The reverse isn't checked — a
# classifier fuzzy-matching a single LICENSE file that mixes multiple
# license texts (e.g. yaml.v3's file, which is explicitly "covered by
# two different licenses: MIT and Apache" per-file) routinely
# under-reports rather than hallucinating extra ones, so NOTICE
# legitimately knowing about more licenses than go-licenses found is
# not a sign NOTICE is wrong.
#
# Bash arrays and nested `while read` loops are deliberately avoided in
# this block: it runs inside the outer `while read ... done <file` loop
# below it, and bash 3.2 (macOS's default /bin/bash) corrupts a nested
# process-substitution/here-string read's file descriptors when nested
# inside an outer loop already bound to a file via `<`. Plain `case`
# substring matching sidesteps that entirely.
license_in_notice() {
  # $1: a single license (e.g. "MIT"); $2: NOTICE's License: value,
  # possibly "A AND B AND C". True if $1 is one of the " AND "-joined
  # parts of $2.
  case " AND $2 AND " in
  *" AND $1 AND "*) return 0 ;;
  *) return 1 ;;
  esac
}

while read -r mod notice_license; do
  [[ -z "$mod" ]] && continue
  reported="$(awk -v m="$mod" '$1 == m { print $2 }' "$tmp/module_licenses.txt" | sort -u)"
  if [[ -z "$reported" ]]; then
    unverified+=("$mod")
    continue
  fi
  for found in $reported; do
    if ! license_in_notice "$found" "$notice_license"; then
      echo "go-licenses reports $found for $mod, which NOTICE's License: value ($notice_license) doesn't include" >&2
      status=1
    fi
  done
done <"$tmp/notice_licenses.txt"

if [[ "${#unverified[@]}" -gt 0 ]]; then
  echo "Note: go-licenses could not classify these NOTICE entries (a classifier limitation, not necessarily a real gap — Part 1 already confirmed each is genuinely reachable) — their License: values were not cross-checked and must be verified by hand if changed:" >&2
  printf '  %s\n' "${unverified[@]-}" >&2
fi

if [[ "$status" -ne 0 ]]; then
  echo >&2
  echo "See CONTRIBUTING.md's 'Updating NOTICE' section." >&2
  exit "$status"
fi

echo "NOTICE is up to date ($(wc -l <"$tmp/used.txt" | tr -d ' ') third-party module(s), $(( $(wc -l <"$tmp/notice_licenses.txt" | tr -d ' ') - ${#unverified[@]} )) license(s) cross-checked)."
