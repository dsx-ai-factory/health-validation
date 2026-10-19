// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"strconv"
	"strings"
)

// DriverVersion is the NVIDIA driver version a node is running, e.g.
// "575.51.03".
//
// It is a node-wide host fact, like the GPU architecture. Raw is the text
// the host reported, which is what an operator sees in nvidia-smi; the
// numeric parts let code order versions, which text cannot ("99.0" sorts
// above "570.0" as a string). Order versions in Go rather than in a CEL
// rule, which would compare them as strings.
//
// Build values with [ParseDriverVersion]: a value without Raw reads as
// unknown whatever its numeric parts.
type DriverVersion struct {
	Raw   string
	Major int
	Minor int
	Patch int
}

// ParseDriverVersion parses a driver version token such as "575.51.03"
// or "570.86" into its numeric parts, keeping the original text.
//
// Returns ok=false unless the token starts with a digits-only
// major.minor pair, so a caller cannot mistake an unreadable version for
// 0.0.0, which would compare as older than every real driver. The patch
// segment is optional and a non-numeric one reads as 0; anything after it
// is ignored.
func ParseDriverVersion(raw string) (DriverVersion, bool) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return DriverVersion{}, false
	}
	major, ok := parseVersionSegment(parts[0])
	if !ok {
		return DriverVersion{}, false
	}
	minor, ok := parseVersionSegment(parts[1])
	if !ok {
		return DriverVersion{}, false
	}
	var patch int
	if len(parts) > 2 {
		patch, _ = parseVersionSegment(parts[2])
	}
	return DriverVersion{Raw: raw, Major: major, Minor: minor, Patch: patch}, true
}

// parseVersionSegment parses one dot-separated segment, accepting only
// digits: strconv.Atoi alone would also take a sign.
func parseVersionSegment(s string) (int, bool) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// Known reports whether this value carries a parsed version. The zero
// value means the driver version could not be determined; callers must
// branch on it rather than treating the zero value as an old driver.
func (v DriverVersion) Known() bool { return v.Raw != "" }

// AtLeast reports whether this driver is at or above the given version.
//
// This is the question every consumer actually asks — an NVML entry
// point added in a particular driver, a check that only applies from
// some release, a decode layout that changed. An unknown version is
// never "at least" anything: a guard reading AtLeast must not open a
// symbol that may be absent just because the version was unreadable.
func (v DriverVersion) AtLeast(major, minor, patch int) bool {
	if !v.Known() {
		return false
	}
	if v.Major != major {
		return v.Major > major
	}
	if v.Minor != minor {
		return v.Minor > minor
	}
	return v.Patch >= patch
}

// String returns the version as the host reported it, or "unknown".
func (v DriverVersion) String() string {
	if !v.Known() {
		return "unknown"
	}
	return v.Raw
}
