#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Runs the health-validation container with the host's journalctl binary and
# its shared-library dependencies bind-mounted in. The image no longer bundles
# systemd; see docs/INTEGRATION.md.
#
# Usage: scripts/health-validation-run.sh <image>[:tag] [health-validation args...]
set -euo pipefail

if [[ $# -lt 1 ]]; then
	echo "usage: $0 <image>[:tag] [health-validation args...]" >&2
	exit 1
fi
image="$1"
shift

journalctl_bin="$(command -v journalctl || true)"
if [[ -z "$journalctl_bin" ]]; then
	echo "error: journalctl not found on PATH" >&2
	exit 1
fi

mounts=(-v "$journalctl_bin:$journalctl_bin:ro")
ldd_output="$(ldd "$journalctl_bin")" || {
	echo "error: could not inspect shared libraries for $journalctl_bin" >&2
	exit 1
}
if grep -Eq '=>[[:space:]]+not found([[:space:]]|$)' <<<"$ldd_output"; then
	echo "error: journalctl has unresolved shared libraries:" >&2
	grep -E '=>[[:space:]]+not found([[:space:]]|$)' <<<"$ldd_output" >&2
	exit 1
fi
while read -r lib; do
	mounts+=(-v "$lib:$lib:ro")
done < <(awk '$3 ~ /^\// && $1 !~ /^(lib(c|pthread|dl|rt|m)\.so)/ {print $3}' <<<"$ldd_output")

exec docker run --rm \
	--gpus all \
	-e NVIDIA_DRIVER_CAPABILITIES=utility \
	-v /run/log/journal:/run/log/journal:ro \
	-v /var/log/journal:/var/log/journal:ro \
	-v /etc/machine-id:/etc/machine-id:ro \
	"${mounts[@]}" \
	"$image" run "$@"
