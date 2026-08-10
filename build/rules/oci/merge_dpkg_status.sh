#!/usr/bin/env bash
# Merge the dpkg databases of a base image and the tar layers stacked on it.
# See dpkg_status.bzl for why this is needed.
set -o pipefail -o errexit -o nounset

readonly bsdtar="$1"
readonly out="$2"
readonly base_layout="$3"
shift 3

# A glibc base image carries far more than this; a smaller count means the
# extraction matched the wrong file and the database would under-report.
readonly min_base_packages=50

# Layers written by docker/OCI builds use `var/...`; tars built by
# rules_distroless use `./var/...`. Non-tar blobs (configs, manifests) fail the
# first read and yield nothing.
extract_status() {
	"$bsdtar" -xOf "$1" var/lib/dpkg/status 2>/dev/null ||
		"$bsdtar" -xOf "$1" ./var/lib/dpkg/status 2>/dev/null ||
		true
}

count_packages() {
	grep -c '^Package: ' "$1" || true
}

# Overlay entries supersede the existing ones for packages present in both,
# because the overlay layer also overwrote those packages' files.
merge_status() {
	awk '
    function pkgname(rec,   i, n, lines) {
      n = split(rec, lines, "\n")
      for (i = 1; i <= n; i++)
        if (lines[i] ~ /^Package: /) { sub(/^Package: /, "", lines[i]); return lines[i] }
      return ""
    }
    BEGIN { RS = ""; ORS = "\n\n" }
    FNR == NR { overlay[++count] = $0; superseded[pkgname($0)] = 1; next }
    !(pkgname($0) in superseded)
    END { for (i = 1; i <= count; i++) print overlay[i] }
  ' "$2" "$1"
}

found=0
: >base.status
for blob in "$base_layout"/blobs/*/*; do
	[ -f "$blob" ] || continue
	extract_status "$blob" >candidate.status
	if [ -s candidate.status ]; then
		found=$((found + 1))
		mv candidate.status base.status
	fi
done

if [ "$found" -ne 1 ]; then
	echo "merge_dpkg_status: want exactly 1 base layer with /var/lib/dpkg/status, got ${found}" >&2
	exit 1
fi

base_packages=$(count_packages base.status)
if [ "$base_packages" -lt "$min_base_packages" ]; then
	echo "merge_dpkg_status: base database has ${base_packages} packages, want >= ${min_base_packages}" >&2
	exit 1
fi

merged=base.status
layer=0
while (($# > 0)); do
	extract_status "$1" >overlay.status
	if [ -s overlay.status ]; then
		layer=$((layer + 1))
		merge_status "$merged" overlay.status >"merged.${layer}"
		merged="merged.${layer}"
	fi
	shift
done

# Guards the merge itself, not just its inputs: a superseding bug shows up as
# either a lost base package or a package listed at two versions.
merged_packages=$(count_packages "$merged")
if [ "$merged_packages" -lt "$base_packages" ]; then
	echo "merge_dpkg_status: merged database lost packages (${base_packages} -> ${merged_packages})" >&2
	exit 1
fi
duplicates=$(grep '^Package: ' "$merged" | sort | uniq -d)
if [ -n "$duplicates" ]; then
	echo "merge_dpkg_status: duplicate entries after merge:" >&2
	echo "$duplicates" >&2
	exit 1
fi

# Fixed timestamp and ownership keep the layer reproducible.
echo "#mtree
./var/lib/dpkg/status type=file uid=0 gid=0 mode=0644 time=1672560000 contents=${merged}
" | "$bsdtar" -cf "$out" "@-"
