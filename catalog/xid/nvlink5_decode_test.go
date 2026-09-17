package xid

import (
	"testing"
)

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
// by ErrorStatus instead. 20 of its 21 rows qualify; digit 0 is
// excluded, see TestDeriveNVLink5SubXidCodes_SkipsAmbiguousDigit.
func TestDeriveNVLink5SubXidCodes(t *testing.T) {
	got := deriveNVLink5SubXidCodes()
	if len(got) != 20 {
		t.Fatalf("len(deriveNVLink5SubXidCodes()) = %d, want 20", len(got))
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

// TestDeriveNVLink5SubXidCodes_SkipsAmbiguousDigit pins the reason
// "149.0" gets no check of its own.
//
// sub_xid_code_counts is keyed from the Xid code and IntrInfo[25:20]
// alone, so it cannot distinguish rows that a decode-table lookup would
// separate by SubComponent or ErrorStatus. Under Xid 149, digit 0 is
// shared by NETIR_LINK_DOWN and NETIR/NETIR_INT (RESET_GPU) and by
// NETIR_BER_EVENT and one NETIR_MFDE_EVENT row (IGNORE). A check keyed
// on it would fire CRITICAL and recommend a GPU reset for a bit-error
// event NVIDIA resolves to IGNORE.
func TestDeriveNVLink5SubXidCodes_SkipsAmbiguousDigit(t *testing.T) {
	for _, c := range deriveNVLink5SubXidCodes() {
		if c.subXidCode == "149.0" {
			t.Errorf("149.0 was derived (%+v); its digit is shared by rows NVIDIA resolves to IGNORE", c)
		}
	}

	// The exclusion must rest on the disagreement, not on the digit
	// being 0: a digit every row under the code resolves alike is kept.
	if !nvlink5SubXidCodeResolvesAlike(149, "4", "RESET_GPU") {
		t.Error("149.4 reported ambiguous; no other Xid 149 row shares digit 4")
	}
	if nvlink5SubXidCodeResolvesAlike(149, "0", "RESET_GPU") {
		t.Error("149.0 reported unambiguous; NETIR_BER_EVENT shares digit 0 and resolves to IGNORE")
	}
}

// TestNVLink5SubXidCodeChecks_NoCheckForAmbiguousDigit is the same
// claim at the registration boundary: no operator-visible check exists
// for a digit whose events cannot be attributed to one row.
func TestNVLink5SubXidCodeChecks_NoCheckForAmbiguousDigit(t *testing.T) {
	for _, def := range Checks() {
		if def.ID == "gpu_xid_149_0" {
			t.Errorf("gpu_xid_149_0 is registered with severity-bearing rules %+v", def.Rules)
		}
	}
}
