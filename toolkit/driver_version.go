// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"os"
	"regexp"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// driverVersionRE extracts a dotted major.minor(.patch) version token
// (e.g. "575.51.03") from an NVIDIA driver version string. Requires at
// least one dot so it doesn't match an unrelated bare number earlier in
// the line — /proc/driver/nvidia/version's "UNIX x86_64" segment would
// otherwise match "86" before ever reaching the real version.
var driverVersionRE = regexp.MustCompile(`\d+(?:\.\d+)+`)

// nvidiaVersionFile is the standard Linux path exposing the loaded
// NVRM module's version string; overridden in tests.
var nvidiaVersionFile = "/proc/driver/nvidia/version"

// DetectDriverVersion reads the host's NVIDIA driver version from
// /proc/driver/nvidia/version, e.g. a line like:
//
//	NVRM version: NVIDIA UNIX x86_64 Kernel Module  575.51.03  Thu Jan  1 00:00:00 UTC 2026
//
// Returns ok=false if the file is unreadable or carries no recognizable
// version — callers must not guess one, since an invented version reads
// as older than every real driver and silently disables anything gated
// on a minimum.
//
// A proc read needs no library, subprocess, timeout, or PATH entry, so it
// works wherever the driver is loaded, whatever state NVML is in.
func DetectDriverVersion() (health.DriverVersion, bool) {
	data, err := os.ReadFile(nvidiaVersionFile)
	if err != nil {
		return health.DriverVersion{}, false
	}
	return parseDriverVersionFile(string(data))
}

// parseDriverVersionFile extracts the version token from the raw
// contents of /proc/driver/nvidia/version and parses it.
func parseDriverVersionFile(contents string) (health.DriverVersion, bool) {
	token := driverVersionRE.FindString(contents)
	if token == "" {
		return health.DriverVersion{}, false
	}
	return health.ParseDriverVersion(token)
}
