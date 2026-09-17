package health_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestParseDriverVersion(t *testing.T) {
	tests := []struct {
		raw                 string
		major, minor, patch int
		wantOK              bool
	}{
		{raw: "575.51.03", major: 575, minor: 51, patch: 3, wantOK: true},
		{raw: "570.86", major: 570, minor: 86, wantOK: true},
		// NVIDIA has shipped four-segment build strings; the parts that
		// order a version are the ones we keep.
		{raw: "580.10.01.2", major: 580, minor: 10, patch: 1, wantOK: true},
		{raw: " 575.51.03 ", major: 575, minor: 51, patch: 3, wantOK: true},
		{raw: "575", wantOK: false},
		{raw: "", wantOK: false},
		{raw: "unknown", wantOK: false},
		{raw: "R575.51", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := health.ParseDriverVersion(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("ParseDriverVersion(%q) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if !ok {
				if got.Known() {
					t.Errorf("a failed parse returned a known version: %+v", got)
				}
				return
			}
			if got.Major != tc.major || got.Minor != tc.minor || got.Patch != tc.patch {
				t.Errorf("ParseDriverVersion(%q) = %d.%d.%d, want %d.%d.%d",
					tc.raw, got.Major, got.Minor, got.Patch, tc.major, tc.minor, tc.patch)
			}
		})
	}
}

// TestParseDriverVersion_KeepsRawText pins that the reported text
// survives parsing: it is what an operator sees in nvidia-smi and what
// a verdict should quote back, so reconstructing it from the parts
// (losing zero padding) is not good enough.
func TestParseDriverVersion_KeepsRawText(t *testing.T) {
	got, ok := health.ParseDriverVersion("575.51.03")
	if !ok {
		t.Fatal("ParseDriverVersion failed on a well-formed version")
	}
	if got.Raw != "575.51.03" {
		t.Errorf("Raw = %q, want 575.51.03 (not the reconstructed 575.51.3)", got.Raw)
	}
	if got.String() != "575.51.03" {
		t.Errorf("String() = %q, want 575.51.03", got.String())
	}
}

// TestDriverVersion_AtLeast covers the comparison every consumer makes.
// The "99 vs 570" rows are the reason this is not string comparison:
// lexicographically "99.0" sorts above "570.0".
func TestDriverVersion_AtLeast(t *testing.T) {
	tests := []struct {
		raw                 string
		major, minor, patch int
		want                bool
	}{
		{"575.51.03", 575, 0, 0, true},
		{"575.51.03", 570, 0, 0, true},
		{"570.86", 575, 0, 0, false},
		{"99.0", 570, 0, 0, false},
		{"570.0", 99, 0, 0, true},
		// Equality holds at every level.
		{"575.51.03", 575, 51, 3, true},
		{"575.51.03", 575, 51, 4, false},
		{"575.51.03", 575, 52, 0, false},
		{"575.52.00", 575, 51, 99, true},
	}
	for _, tc := range tests {
		v, ok := health.ParseDriverVersion(tc.raw)
		if !ok {
			t.Fatalf("ParseDriverVersion(%q) failed", tc.raw)
		}
		if got := v.AtLeast(tc.major, tc.minor, tc.patch); got != tc.want {
			t.Errorf("%q.AtLeast(%d, %d, %d) = %v, want %v",
				tc.raw, tc.major, tc.minor, tc.patch, got, tc.want)
		}
	}
}

// TestDriverVersion_UnknownIsNeverAtLeast is the safety property. An
// unreadable version must not read as newer than a gate: a guard using
// AtLeast to decide whether an NVML entry point exists would then open
// a symbol that may be absent.
func TestDriverVersion_UnknownIsNeverAtLeast(t *testing.T) {
	var zero health.DriverVersion
	if zero.Known() {
		t.Error("the zero DriverVersion reports Known() = true")
	}
	if zero.AtLeast(0, 0, 0) {
		t.Error("an unknown version reported AtLeast(0, 0, 0) = true; it must gate closed")
	}
	if zero.String() != "unknown" {
		t.Errorf("String() = %q, want unknown", zero.String())
	}
}
