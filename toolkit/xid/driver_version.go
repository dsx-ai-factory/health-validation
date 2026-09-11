package xid

import (
	"os"
	"regexp"
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

// DetectDriverVersion reads the host's NVIDIA driver version string
// (e.g. "575.51.03") from /proc/driver/nvidia/version. Returns
// ok=false if the version file is unreadable or its content doesn't
// contain a recognizable version — callers must not guess a version in
// that case.
//
// This is a raw host fact, not an interpretation of it: classifying a
// version string into an IntrInfo bit-layout era for NVLink5 (Xid
// 144-150) resolution decoding is catalog knowledge — see
// catalog/xid.DriverEraFromVersion.
func DetectDriverVersion() (string, bool) {
	version, err := readDriverVersion()
	if err != nil {
		return "", false
	}
	return version, true
}

// readDriverVersion reads the NVRM kernel module's version string from
// /proc/driver/nvidia/version, e.g. a line like:
//
//	NVRM version: NVIDIA UNIX x86_64 Kernel Module  575.51.03  Thu Jan  1 00:00:00 UTC 2026
//
// Deliberately does not shell out to nvidia-smi: this file is a static
// proc read with no subprocess, timeout, or PATH dependency, and every
// host with the driver loaded exposes it.
func readDriverVersion() (string, error) {
	data, err := os.ReadFile(nvidiaVersionFile)
	if err != nil {
		return "", err
	}
	return parseDriverVersion(string(data))
}

// parseDriverVersion extracts the version token from the raw contents
// of /proc/driver/nvidia/version.
func parseDriverVersion(contents string) (string, error) {
	m := driverVersionRE.FindString(contents)
	if m == "" {
		return "", errNoDriverVersion
	}
	return m, nil
}

var errNoDriverVersion = versionParseError("no NVIDIA driver version found")

type versionParseError string

func (e versionParseError) Error() string { return string(e) }
