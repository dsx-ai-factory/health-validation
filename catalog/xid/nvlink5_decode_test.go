package xid

import (
	"strings"
	"testing"
)

func TestDriverEraFromVersion(t *testing.T) {
	tests := []struct {
		version string
		want    DriverEra
	}{
		{"575.51.03", DriverEraV2},
		{"576.02.01", DriverEraV2},
		{"574.99.99", DriverEraV1},
		{"570.86.10", DriverEraV1},
		{"", DriverEraUnknown},
		{"not-a-version", DriverEraUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := DriverEraFromVersion(tt.version); got != tt.want {
				t.Errorf("DriverEraFromVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

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

func TestSubcodeMatches(t *testing.T) {
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
		if got := subcodeMatches(tt.log, tt.table); got != tt.want {
			t.Errorf("subcodeMatches(%q, %q) = %v, want %v", tt.log, tt.table, got, tt.want)
		}
	}
}

// TestDecodeNVLink5Resolution_ExactErrorStatusMatch covers a
// sub-component NVIDIA's table disambiguates purely by ErrorStatus
// (no driver era needed): Xid 148's single documented row.
func TestDecodeNVLink5Resolution_ExactErrorStatusMatch(t *testing.T) {
	detail := NVLink5Detail{SubComponent: "NVLPW_CTRL", ErrorStatus: "0x80000000", Fatal: false}
	got := DecodeNVLink5Resolution(148, DriverEraUnknown, detail)

	if !got.Matched {
		t.Fatalf("Matched = false, want true (ErrorStatus alone disambiguates, no era needed)")
	}
	if got.Resolution != "IGNORE" || got.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("Resolution/Investigation = %q/%q, want IGNORE/CONTACT_SUPPORT", got.Resolution, got.Investigation)
	}
}

// TestDecodeNVLink5Resolution_ErrorStatusDifferentiatesSeverity covers
// a sub-component where the SAME ErrorStatus family has both an
// IGNORE and a RESET_GPU row depending on the exact hex value (Xid
// 144 SAW_MVB) — makes sure we don't collapse distinct ErrorStatus
// values into one answer.
func TestDecodeNVLink5Resolution_ErrorStatusDifferentiatesSeverity(t *testing.T) {
	ignore := DecodeNVLink5Resolution(144, DriverEraUnknown, NVLink5Detail{SubComponent: "SAW_MVB", ErrorStatus: "0x00000001", Fatal: false})
	if !ignore.Matched || ignore.Resolution != "IGNORE" {
		t.Errorf("ErrorStatus 0x1: Resolution = %q matched=%v, want IGNORE/true", ignore.Resolution, ignore.Matched)
	}

	reset := DecodeNVLink5Resolution(144, DriverEraUnknown, NVLink5Detail{SubComponent: "SAW_MVB", ErrorStatus: "0x00000002", Fatal: true})
	if !reset.Matched || reset.Resolution != "RESET_GPU" {
		t.Errorf("ErrorStatus 0x2: Resolution = %q matched=%v, want RESET_GPU/true", reset.Resolution, reset.Matched)
	}
}

// TestDecodeNVLink5Resolution_RequiresEraWhenAmbiguous covers Xid 149's
// NETIR_LINK_EVT/NETIR_LINK_DOWN family, which NVIDIA's table
// disambiguates only by IntrInfo bit pattern (ErrorStatus is blank for
// every row). Unknown era must not guess.
func TestDecodeNVLink5Resolution_RequiresEraWhenAmbiguous(t *testing.T) {
	detail := NVLink5Detail{
		SubComponent: "NETIR_LINK_EVT",
		// The exact value doesn't matter for this test — DriverEraUnknown
		// must skip pattern matching entirely regardless of IntrInfo.
		IntrInfo: "0x00000091",
		Fatal:    true,
	}
	got := DecodeNVLink5Resolution(149, DriverEraUnknown, detail)
	if got.Matched {
		t.Errorf("Matched = true with DriverEraUnknown, want false (must not guess which era's bit layout applies)")
	}
	if got.Resolution != "RESET_GPU" || got.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("fallback Resolution/Investigation = %q/%q, want the Fatal-derived RESET_GPU/CONTACT_SUPPORT", got.Resolution, got.Investigation)
	}
}

// TestDecodeNVLink5Resolution_NoMatchFallsBackToFatalFlag covers a
// sub-component/ErrorStatus this repo's table doesn't carry at all.
func TestDecodeNVLink5Resolution_NoMatchFallsBackToFatalFlag(t *testing.T) {
	fatal := DecodeNVLink5Resolution(144, DriverEraUnknown, NVLink5Detail{SubComponent: "UNKNOWN_FUTURE_SUBCODE", ErrorStatus: "0x12345678", Fatal: true})
	if fatal.Matched {
		t.Errorf("Matched = true for an unrecognized sub-component, want false")
	}
	if fatal.Resolution != "RESET_GPU" || fatal.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("fallback (Fatal) = %q/%q, want RESET_GPU/CONTACT_SUPPORT", fatal.Resolution, fatal.Investigation)
	}

	nonfatal := DecodeNVLink5Resolution(144, DriverEraUnknown, NVLink5Detail{SubComponent: "UNKNOWN_FUTURE_SUBCODE", ErrorStatus: "0x12345678", Fatal: false})
	if nonfatal.Resolution != "IGNORE" {
		t.Errorf("fallback (Nonfatal) Resolution = %q, want IGNORE", nonfatal.Resolution)
	}
}

// TestDecodeNVLink5Resolution_EndToEnd exercises decoding against the
// real-world example log line used elsewhere in this package, across
// each driver era. Regardless of era, the result must never be a
// fabricated guess: it's either a confirmed table match or the
// conservative Fatal-derived fallback.
func TestDecodeNVLink5Resolution_EndToEnd(t *testing.T) {
	detail := NVLink5Detail{
		SubComponent: "NETIR_LINK_EVT",
		IntrInfo:     "0x004505c6",
		ErrorStatus:  "0x00000000",
		Fatal:        true,
	}

	for _, era := range []DriverEra{DriverEraUnknown, DriverEraV1, DriverEraV2} {
		got := DecodeNVLink5Resolution(149, era, detail)
		// Fatal is true in this line, so whether or not a table row
		// matched, the immediate action must be RESET_GPU: either
		// because that's what the matched row says, or because that's
		// the Fatal-derived fallback.
		if got.Resolution != "RESET_GPU" {
			t.Errorf("era %v: Resolution = %q, want RESET_GPU either way for a Fatal event", era, got.Resolution)
		}
		if era == DriverEraUnknown && got.Matched {
			t.Errorf("era %v: Matched = true, want false (must not guess with no known era)", era)
		}
	}
}

// TestDecodeNVLink5Resolution_Action2Escalation covers Xid 145
// RLW_RXPIPE, ErrorStatus 0x1/0x2/0x4: NVIDIA's source table marks
// these "promoteable" ('*') — normally IGNORE, but escalates to
// RESET_GPU if a second, more specific IntrInfo bit is also set. Values
// computed directly from the table's V2 bit patterns.
func TestDecodeNVLink5Resolution_Action2Escalation(t *testing.T) {
	base := DecodeNVLink5Resolution(145, DriverEraV2, NVLink5Detail{SubComponent: "RLW_RXPIPE", ErrorStatus: "0x00000001", IntrInfo: "0x00000006", Fatal: false})
	if !base.Matched || base.Resolution != "IGNORE" {
		t.Errorf("base pattern: Resolution = %q matched=%v, want IGNORE/true", base.Resolution, base.Matched)
	}

	escalated := DecodeNVLink5Resolution(145, DriverEraV2, NVLink5Detail{SubComponent: "RLW_RXPIPE", ErrorStatus: "0x00000001", IntrInfo: "0x00000086", Fatal: false})
	if !escalated.Matched || escalated.Resolution != "RESET_GPU" {
		t.Errorf("action2 pattern: Resolution = %q matched=%v, want RESET_GPU/true (escalated)", escalated.Resolution, escalated.Matched)
	}
	if escalated.Investigation != "CONTACT_SUPPORT" {
		t.Errorf("action2 pattern: Investigation = %q, want CONTACT_SUPPORT (unchanged by escalation)", escalated.Investigation)
	}

	// A sub-component with no action2 data must be unaffected regardless
	// of IntrInfo content.
	noAction2 := DecodeNVLink5Resolution(148, DriverEraV2, NVLink5Detail{SubComponent: "NVLPW_CTRL", ErrorStatus: "0x80000000", IntrInfo: "0xFFFFFFFF", Fatal: false})
	if noAction2.Resolution != "IGNORE" {
		t.Errorf("no-action2 row: Resolution = %q, want IGNORE (no escalation table for this row)", noAction2.Resolution)
	}
}

func TestNvlink5SubXidCodeDigit(t *testing.T) {
	tests := []struct {
		v1Pattern string
		want      string
		wantOK    bool
	}{
		{"------000100----------0111000110", "4", true},
		{"------001010----------0111000110", "A", true},
		{"------101011----------0111000110", "2B", true},
		{"------000000----------0000100001", "0", true},
		{"too-short", "", false},
	}
	for _, tt := range tests {
		got, ok := nvlink5SubXidCodeDigit(nvlink5DecodeRow{v1Pattern: tt.v1Pattern})
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("nvlink5SubXidCodeDigit(%q) = (%q, %v), want (%q, %v)", tt.v1Pattern, got, ok, tt.want, tt.wantOK)
		}
	}
}

// TestDeriveNVLink5SubXidCodes locks in that exactly Xid 149's
// NETIR_LINK_EVT/NETIR_LINK_DOWN family (the only (code, subcode) group
// in nvlink5DecodeTable with a blank ErrorStatus and more than one
// row) gets per-SubXidCode checks — every other group is disambiguated
// by ErrorStatus instead.
func TestDeriveNVLink5SubXidCodes(t *testing.T) {
	got := deriveNVLink5SubXidCodes()
	if len(got) != 21 {
		t.Fatalf("len(deriveNVLink5SubXidCodes()) = %d, want 21", len(got))
	}
	for _, c := range got {
		if c.subcode != "NETIR_LINK_EVT/NETIR_LINK_DOWN" {
			t.Errorf("subcode = %q, want NETIR_LINK_EVT/NETIR_LINK_DOWN for every entry (subXidCode %s)", c.subcode, c.subXidCode)
		}
	}

	byCode := make(map[string]nvlink5SubXidCode, len(got))
	for _, c := range got {
		byCode[c.subXidCode] = c
	}
	four, ok := byCode["149.4"]
	if !ok {
		t.Fatalf("149.4 not derived")
	}
	if !four.critical || four.investigatory != "INVESTIGATE_LINK_SI" {
		t.Errorf("149.4 = %+v, want critical=true investigatory=INVESTIGATE_LINK_SI", four)
	}
	twentysix, ok := byCode["149.26"]
	if !ok {
		t.Fatalf("149.26 not derived")
	}
	if twentysix.critical {
		t.Errorf("149.26 critical = true, want false (decode table resolution is IGNORE)")
	}
}
