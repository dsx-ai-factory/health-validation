#!/usr/bin/env bash
#
# Verifies that NOTICE lists every third-party Go module this repo actually
# uses (across all four go.work modules, including test-only dependencies),
# each at its currently-resolved version.
#
# This does not classify license text: adding a genuinely new dependency
# still requires a human to read its LICENSE file and pick the right SPDX
# identifier for the new NOTICE entry (see CONTRIBUTING.md). This script
# only catches the mechanical drift that happens on every version bump: a
# module NOTICE has forgotten, or a version NOTICE hasn't caught up to.
#
# Usage:
#   tools/check_notice.sh              # uses "go" on PATH
#   BAZEL_GO=1 tools/check_notice.sh   # uses Bazel's pinned Go SDK (CI)
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

run_go() {
  if [[ "${BAZEL_GO:-}" == "1" ]]; then
    bazel run //tools/go -- "$@"
  else
    go "$@"
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Every module reachable from any package this repo builds (including
# _test.go files, via -test), across all four go.work modules, excluding
# our own in-repo modules. Forced to GOOS=linux and CGO_ENABLED=1: the
# container only ships for Linux, and several packages (toolkit/nvml,
# collect/source/dcgm) are gated behind "linux" and "linux && cgo" build
# constraints that a plain Darwin host build would silently skip,
# under-reporting what's actually compiled in.
for mod_dir in agentkit catalog collect toolkit; do
  (cd "$mod_dir" && GOOS=linux CGO_ENABLED=1 run_go list -deps -test \
    -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' ./...)
done |
  grep -v '^github.com/dsx-ai-factory/health-validation' |
  sort -u -k1,1 >"$tmp/used.txt"

# What NOTICE's "Go module dependencies:" section currently documents.
# Module lines look like "  <path>  <version>"; skip the License:/URL:
# lines that follow each one, and stop before the "Bundled source
# modifications" section (which isn't Go-module-shaped at all).
awk '
  /^Go module dependencies:$/ { in_section = 1; next }
  /^---$/ { in_section = 0 }
  in_section && /^  [^ ]/ {
    n = split($0, f, /  +/)
    if (n >= 3 && f[2] != "License:" && f[2] != "URL:") print f[2], f[3]
  }
' NOTICE | sort -u -k1,1 >"$tmp/notice.txt"

status=0

missing="$(comm -23 <(cut -d' ' -f1 "$tmp/used.txt") <(cut -d' ' -f1 "$tmp/notice.txt"))"
if [[ -n "$missing" ]]; then
  echo "NOTICE is missing an entry for:" >&2
  echo "$missing" | sed 's/^/  /' >&2
  echo >&2
  status=1
fi

while read -r mod ver; do
  [[ -z "$mod" ]] && continue
  notice_ver="$(awk -v m="$mod" '$1 == m { print $2 }' "$tmp/notice.txt")"
  if [[ -n "$notice_ver" && "$notice_ver" != "$ver" ]]; then
    echo "NOTICE has a stale version for $mod: NOTICE has $notice_ver, resolved to $ver" >&2
    status=1
  fi
done <"$tmp/used.txt"

if [[ "$status" -ne 0 ]]; then
  echo >&2
  echo "See CONTRIBUTING.md's 'Updating NOTICE' section." >&2
  exit "$status"
fi

echo "NOTICE is up to date ($(wc -l <"$tmp/used.txt" | tr -d ' ') third-party module(s))."
