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
