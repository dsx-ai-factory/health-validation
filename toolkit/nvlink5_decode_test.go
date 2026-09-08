package toolkit

import (
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func TestMatchesPattern(t *testing.T) {
	allWildcard := strings.Repeat("-", 32)
	if !matchesPattern(0xFFFFFFFF, allWildcard) {
		t.Errorf("matchesPattern(0xFFFFFFFF, all-wildcard) = false, want true")
	}

	tooShort := strings.Repeat("0", 31)
	if matchesPattern(0, tooShort) {
		t.Errorf("matchesPattern with a 31-char pattern = true, want false (wrong length)")
	}
	tooLong := strings.Repeat("0", 33)
	if matchesPattern(0, tooLong) {
		t.Errorf("matchesPattern with a 33-char pattern = true, want false (wrong length)")
	}

	// A 32-char pattern requiring bit 0 (LSB, last char) to be 1 and bit
	// 31 (MSB, first char) to be 0; everything else wildcard.
	pattern := "-------------------------------1"
	if !matchesPattern(0x00000001, pattern) {
		t.Errorf("matchesPattern(0x1, %q) = false, want true", pattern)
	}
	if matchesPattern(0x00000000, pattern) {
		t.Errorf("matchesPattern(0x0, %q) = true, want false", pattern)
	}
}

func TestParseHexUint32(t *testing.T) {
	tests := []struct {
		in     string
		want   uint32
		wantOK bool
	}{
		{"0x004505c6", 0x004505c6, true},
		{"0X4505C6", 0x4505c6, true},
		{"", 0, false},
		{"not-hex", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseHexUint32(tt.in)
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("parseHexUint32(%q) = (%#x, %v), want (%#x, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestSubComponentMatches(t *testing.T) {
	tests := []struct {
		log, table string
		want       bool
	}{
		{"TLW_RX_PIPE0", "TLW_RX/TLW_RX_PIPE0", true},
		{"TLW_RX", "TLW_RX/TLW_RX_PIPE0", true},
		{"TLW_RX_PIPE1", "TLW_RX/TLW_RX_PIPE0", false},
		{"MSE_DEGRADED", "MSE Degraded", true},
		{"NETIR_LINK_DOWN", "NETIR_LINK_EVT/NETIR_LINK_DOWN", true},
	}
	for _, tt := range tests {
		if got := subComponentMatches(tt.log, tt.table); got != tt.want {
			t.Errorf("subComponentMatches(%q, %q) = %v, want %v", tt.log, tt.table, got, tt.want)
		}
	}
}

// TestDecodeNVLink5Resolution_ExactErrorStatusMatch covers a
// sub-component NVIDIA's table disambiguates purely by ErrorStatus
// (no driver era needed): Xid 148's single documented row.
func TestDecodeNVLink5Resolution_ExactErrorStatusMatch(t *testing.T) {
	detail := &xid.NVLink5Detail{SubComponent: "NVLPW_CTRL", ErrorStatus: "0x80000000", Fatal: false}
	decodeNVLink5Resolution(148, DriverEraUnknown, detail)

	if !detail.ResolutionMatched {
		t.Fatalf("ResolutionMatched = false, want true (ErrorStatus alone disambiguates, no era needed)")
	}
	if detail.Resolution != "IGNORE" || detail.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("Resolution/Investigation = %q/%q, want IGNORE/CONTACT_SUPPORT", detail.Resolution, detail.Investigation)
	}
}

// TestDecodeNVLink5Resolution_ErrorStatusDifferentiatesSeverity covers
// a sub-component where the SAME ErrorStatus family has both an
// IGNORE and a RESET_GPU row depending on the exact hex value (Xid
// 144 SAW_MVB) — makes sure we don't collapse distinct ErrorStatus
// values into one answer.
func TestDecodeNVLink5Resolution_ErrorStatusDifferentiatesSeverity(t *testing.T) {
	ignore := &xid.NVLink5Detail{SubComponent: "SAW_MVB", ErrorStatus: "0x00000001", Fatal: false}
	decodeNVLink5Resolution(144, DriverEraUnknown, ignore)
	if !ignore.ResolutionMatched || ignore.Resolution != "IGNORE" {
		t.Errorf("ErrorStatus 0x1: Resolution = %q matched=%v, want IGNORE/true", ignore.Resolution, ignore.ResolutionMatched)
	}

	reset := &xid.NVLink5Detail{SubComponent: "SAW_MVB", ErrorStatus: "0x00000002", Fatal: true}
	decodeNVLink5Resolution(144, DriverEraUnknown, reset)
	if !reset.ResolutionMatched || reset.Resolution != "RESET_GPU" {
		t.Errorf("ErrorStatus 0x2: Resolution = %q matched=%v, want RESET_GPU/true", reset.Resolution, reset.ResolutionMatched)
	}
}

// TestDecodeNVLink5Resolution_RequiresEraWhenAmbiguous covers Xid 149's
// NETIR_LINK_EVT/NETIR_LINK_DOWN family, which NVIDIA's table
// disambiguates only by IntrInfo bit pattern (ErrorStatus is blank for
// every row). Unknown era must not guess.
func TestDecodeNVLink5Resolution_RequiresEraWhenAmbiguous(t *testing.T) {
	detail := &xid.NVLink5Detail{
		SubComponent: "NETIR_LINK_EVT",
		// The exact value doesn't matter for this test — DriverEraUnknown
		// must skip pattern matching entirely regardless of IntrInfo.
		IntrInfo: "0x00000091",
		Fatal:    true,
	}
	decodeNVLink5Resolution(149, DriverEraUnknown, detail)
	if detail.ResolutionMatched {
		t.Errorf("ResolutionMatched = true with DriverEraUnknown, want false (must not guess which era's bit layout applies)")
	}
	if detail.Resolution != "RESET_GPU" || detail.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("fallback Resolution/Investigation = %q/%q, want the Fatal-derived RESET_GPU/CONTACT_SUPPORT", detail.Resolution, detail.Investigation)
	}
}

// TestDecodeNVLink5Resolution_NoMatchFallsBackToFatalFlag covers a
// sub-component/ErrorStatus this repo's table doesn't carry at all.
func TestDecodeNVLink5Resolution_NoMatchFallsBackToFatalFlag(t *testing.T) {
	fatal := &xid.NVLink5Detail{SubComponent: "UNKNOWN_FUTURE_SUBCODE", ErrorStatus: "0x12345678", Fatal: true}
	decodeNVLink5Resolution(144, DriverEraUnknown, fatal)
	if fatal.ResolutionMatched {
		t.Errorf("ResolutionMatched = true for an unrecognized sub-component, want false")
	}
	if fatal.Resolution != "RESET_GPU" || fatal.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("fallback (Fatal) = %q/%q, want RESET_GPU/CONTACT_SUPPORT", fatal.Resolution, fatal.Investigation)
	}

	nonfatal := &xid.NVLink5Detail{SubComponent: "UNKNOWN_FUTURE_SUBCODE", ErrorStatus: "0x12345678", Fatal: false}
	decodeNVLink5Resolution(144, DriverEraUnknown, nonfatal)
	if nonfatal.Resolution != "IGNORE" {
		t.Errorf("fallback (Nonfatal) Resolution = %q, want IGNORE", nonfatal.Resolution)
	}
}

// TestParseXid_NVLink5ResolutionEndToEnd exercises the full path
// (ParseXid -> parseNVLink5Line -> decodeNVLink5Resolution) against the
// real-world example log line used elsewhere in this package, across
// each driver era. Regardless of era, the result must never be a
// fabricated guess: it's either a confirmed table match or the
// conservative Fatal-derived fallback.
func TestParseXid_NVLink5ResolutionEndToEnd(t *testing.T) {
	line := "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000 0x00000001 0x00000002)"

	for _, era := range []DriverEra{DriverEraUnknown, DriverEraV1, DriverEraV2} {
		m := ParseXid("boot", []string{line}, era)
		detail := m.Events[0].NVLink5
		if detail == nil {
			t.Fatalf("era %v: NVLink5 = nil", era)
		}
		if detail.DriverEra != era.String() {
			t.Errorf("era %v: DriverEra recorded = %q, want %q", era, detail.DriverEra, era.String())
		}
		// Fatal is true in this line, so whether or not a table row
		// matched, the immediate action must be RESET_GPU: either
		// because that's what the matched row says, or because that's
		// the Fatal-derived fallback.
		if detail.Resolution != "RESET_GPU" {
			t.Errorf("era %v: Resolution = %q, want RESET_GPU either way for a Fatal event", era, detail.Resolution)
		}
		if era == DriverEraUnknown && detail.ResolutionMatched {
			t.Errorf("era %v: ResolutionMatched = true, want false (must not guess with no known era)", era)
		}
	}
}

// TestDecodeNVLink5Resolution_Action2Escalation covers Xid 145
// RLW_RXPIPE, ErrorStatus 0x1/0x2/0x4: NVIDIA's source table marks
// these "promoteable" ('*') — normally IGNORE, but escalates to
// RESET_GPU if a second, more specific IntrInfo bit is also set. Values
// computed directly from the table's V2 bit patterns.
func TestDecodeNVLink5Resolution_Action2Escalation(t *testing.T) {
	base := &xid.NVLink5Detail{SubComponent: "RLW_RXPIPE", ErrorStatus: "0x00000001", IntrInfo: "0x00000006", Fatal: false}
	decodeNVLink5Resolution(145, DriverEraV2, base)
	if !base.ResolutionMatched || base.Resolution != "IGNORE" {
		t.Errorf("base pattern: Resolution = %q matched=%v, want IGNORE/true", base.Resolution, base.ResolutionMatched)
	}

	escalated := &xid.NVLink5Detail{SubComponent: "RLW_RXPIPE", ErrorStatus: "0x00000001", IntrInfo: "0x00000086", Fatal: false}
	decodeNVLink5Resolution(145, DriverEraV2, escalated)
	if !escalated.ResolutionMatched || escalated.Resolution != "RESET_GPU" {
		t.Errorf("action2 pattern: Resolution = %q matched=%v, want RESET_GPU/true (escalated)", escalated.Resolution, escalated.ResolutionMatched)
	}
	if escalated.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("action2 pattern: Investigation = %q, want CONTACT_SUPPORT (unchanged by escalation)", escalated.Investigation)
	}

	// A sub-component with no action2 data must be unaffected regardless
	// of IntrInfo content.
	noAction2 := &xid.NVLink5Detail{SubComponent: "NVLPW_CTRL", ErrorStatus: "0x80000000", IntrInfo: "0xFFFFFFFF", Fatal: false}
	decodeNVLink5Resolution(148, DriverEraV2, noAction2)
	if noAction2.Resolution != "IGNORE" {
		t.Errorf("no-action2 row: Resolution = %q, want IGNORE (no escalation table for this row)", noAction2.Resolution)
	}
}

func TestDecodeNVLink5Resolution_RecordsDriverEra(t *testing.T) {
	detail := &xid.NVLink5Detail{SubComponent: "TREX", ErrorStatus: "0x00000001"}
	decodeNVLink5Resolution(147, DriverEraV2, detail)
	if detail.DriverEra != "v2(>=R575)" {
		t.Errorf("DriverEra = %q, want v2(>=R575)", detail.DriverEra)
	}
}
