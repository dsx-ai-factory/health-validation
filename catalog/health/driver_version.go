package health

import (
	"strconv"
	"strings"
)

// DriverVersion is the NVIDIA driver version a node is running, e.g.
// "575.51.03".
//
// It is a node-wide host fact, like the GPU architecture: discovered
// once per run and true for every sensor and check on that host. Both
// the text the host reported and its numeric parts are kept. Raw is
// what a verdict or signal should surface, because it is what an
// operator will see in nvidia-smi and in NVIDIA's own release notes;
// the numeric parts exist so code can order versions, which text
// cannot do — "99.0" sorts above "570.0" as a string.
//
// Version ordering therefore belongs in Go, never in a CEL rule: the
// evaluator declares every signal as a dynamic type and would compare
// two version signals lexicographically without complaint.
type DriverVersion struct {
	Raw   string
	Major int
	Minor int
	Patch int
}

// ParseDriverVersion parses a driver version token such as "575.51.03"
// or "570.86" into its numeric parts, keeping the original text.
//
// Returns ok=false when the token has no numeric major.minor pair, so a
// caller cannot mistake an unreadable version for 0.0.0 — which would
// compare as older than every real driver and silently disable
// everything gated on a minimum. A trailing patch segment is optional;
// anything beyond it is ignored, since NVIDIA has shipped four-segment
// build strings.
func ParseDriverVersion(raw string) (DriverVersion, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) < 2 {
		return DriverVersion{}, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return DriverVersion{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return DriverVersion{}, false
	}
	var patch int
	if len(parts) > 2 {
		// A non-numeric patch is not fatal: major.minor already orders
		// the version, and NVIDIA's release notes key on those.
		patch, _ = strconv.Atoi(parts[2])
	}
	return DriverVersion{Raw: strings.TrimSpace(raw), Major: major, Minor: minor, Patch: patch}, true
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
