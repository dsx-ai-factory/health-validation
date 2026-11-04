#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

# Runs the health-validation container with the host's journalctl binary and
# DCGM client library, and their shared-library dependencies, bind-mounted in,
# on the host network so the CLI reaches the host's DCGM hostengine on
# localhost, and in the host PID namespace so gpu_compute_apps sees other
# workloads' compute processes. The image bundles neither systemd nor DCGM; see
# docs/integration.md.
#
# Usage: scripts/health-validation-run.sh <image>[:tag] [health-validation args...]
set -euo pipefail

if [[ $# -lt 1 ]]; then
	echo "usage: $0 <image>[:tag] [health-validation args...]" >&2
	exit 1
fi
image="$1"
shift

mounts=()
declare -A mounted=()

mount_ro() {
	if [[ -z ${mounted[$1]:-} ]]; then
		mounted[$1]=1
		mounts+=(-v "$1:$1:ro")
	fi
}

# mount_with_libs bind-mounts path and its shared-library dependencies, except
# the C library family the image provides.
mount_with_libs() {
	local path="$1" ldd_output
	mount_ro "$path"
	ldd_output="$(ldd "$path")" || {
		echo "error: could not inspect shared libraries for $path" >&2
		exit 1
	}
	if grep -Eq '=>[[:space:]]+not found([[:space:]]|$)' <<<"$ldd_output"; then
		echo "error: $path has unresolved shared libraries:" >&2
		grep -E '=>[[:space:]]+not found([[:space:]]|$)' <<<"$ldd_output" >&2
		exit 1
	fi
	while read -r lib; do
		mount_ro "$lib"
	done < <(awk '$3 ~ /^\// && $1 !~ /^(lib(c|pthread|dl|rt|m)\.so)/ {print $3}' <<<"$ldd_output")
}

journalctl_bin="$(command -v journalctl || true)"
if [[ -z "$journalctl_bin" ]]; then
	echo "error: journalctl not found on PATH" >&2
	exit 1
fi
mount_with_libs "$journalctl_bin"

# awk reads all of ldconfig's output: exiting early could SIGPIPE ldconfig and
# fail the pipeline.
libdcgm="$(ldconfig -p | awk '$1 == "libdcgm.so.4" && !found {print $NF; found = 1}')"
dcgm_env=()
if [[ -n "$libdcgm" ]]; then
	mount_with_libs "$libdcgm"
	dcgm_env=(-e LD_LIBRARY_PATH="$(dirname "$libdcgm")")
else
	# Run anyway: the DCGM-backed GPU checks report a collection failure and
	# every other check still runs.
	echo "warning: libdcgm.so.4 not found; the DCGM-backed GPU checks will report a collection failure (install DCGM 4)" >&2
fi

# Needed only to record threshold overrides. Skip it when absent: docker -v
# would create the missing path on the host as a directory.
if [[ -S /run/systemd/journal/socket ]]; then
	mounts+=(-v /run/systemd/journal/socket:/run/systemd/journal/socket:ro)
fi

exec docker run --rm \
	--gpus all \
	--network host \
	--pid host \
	-e NVIDIA_DRIVER_CAPABILITIES=utility \
	${dcgm_env[@]+"${dcgm_env[@]}"} \
	-v /run/log/journal:/run/log/journal:ro \
	-v /var/log/journal:/var/log/journal:ro \
	-v /etc/machine-id:/etc/machine-id:ro \
	"${mounts[@]}" \
	"$image" run "$@"
