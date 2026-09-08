package xid

import (
	"os"
	"regexp"
	"strconv"
)

// DriverEra distinguishes the two IntrInfo bit layouts NVIDIA's Xid
// 144-150 decode table uses: pre-R575 drivers pack the fault subcode
// into IntrInfo[9:5], R575-and-later drivers use IntrInfo[6:0] instead.
// A verdict decoded under the wrong era would read the wrong bits, so
// DriverEraUnknown is the safe default whenever the version can't be
// determined — callers must not guess.
type DriverEra int

const (
	// DriverEraUnknown means the driver version could not be read or
	// parsed. NVLink5 subcode decoding that depends on IntrInfo bit
	// position must not proceed under this era.
	DriverEraUnknown DriverEra = iota
	// DriverEraV1 is a driver older than R575.
	DriverEraV1
	// DriverEraV2 is R575 or newer.
	DriverEraV2
)

func (e DriverEra) String() string {
	switch e {
	case DriverEraV1:
		return "v1(<R575)"
	case DriverEraV2:
		return "v2(>=R575)"
	default:
		return "unknown"
	}
}

// nvidiaDriverEraThreshold is the first major driver version using the
// V2 IntrInfo[6:0] encoding.
const nvidiaDriverEraThreshold = 575

// driverVersionRE extracts a dotted major.minor(.patch) version token
// (e.g. "575.51.03") from an NVIDIA driver version string. Requires at
// least one dot so it doesn't match an unrelated bare number earlier in
// the line — /proc/driver/nvidia/version's "UNIX x86_64" segment would
// otherwise match "86" before ever reaching the real version.
var driverVersionRE = regexp.MustCompile(`\d+(?:\.\d+)+`)

// nvidiaVersionFile is the standard Linux path exposing the loaded
// NVRM module's version string; overridden in tests.
var nvidiaVersionFile = "/proc/driver/nvidia/version"

// DetectDriverEra reads the host's NVIDIA driver version and classifies
// it into the IntrInfo bit-layout era NVIDIA's Xid 144-150 decode table
// uses. Returns DriverEraUnknown (never guesses) if the version file is
// unreadable or its content doesn't contain a recognizable version.
func DetectDriverEra() DriverEra {
	version, err := readDriverVersion()
	if err != nil {
		return DriverEraUnknown
	}
	return driverEraFromVersion(version)
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

// driverEraFromVersion classifies a driver version string (e.g.
// "575.51.03") by its major version number against
// [nvidiaDriverEraThreshold]. Returns DriverEraUnknown if the major
// version isn't parseable.
func driverEraFromVersion(version string) DriverEra {
	major := driverVersionRE.FindString(version)
	if major == "" {
		return DriverEraUnknown
	}
	// driverVersionRE's own match may include minor/patch segments;
	// take just the leading numeric run for the major version.
	end := 0
	for end < len(major) && major[end] >= '0' && major[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(major[:end])
	if err != nil {
		return DriverEraUnknown
	}
	if n >= nvidiaDriverEraThreshold {
		return DriverEraV2
	}
	return DriverEraV1
}
