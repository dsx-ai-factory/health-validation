package xid

import "testing"

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

func TestNvlink5SubXidCode(t *testing.T) {
	tests := []struct {
		intrInfo string
		want     string
		code     int64
		wantOK   bool
	}{
		// Bits 20-25 = 000100 = 4.
		{"0x004505c6", "149.4", 149, true},
		// Bits 20-25 all zero.
		{"0x00000091", "149.0", 149, true},
		// Bits 20-25 = 001010 = 0xA.
		{"0x00A00000", "149.A", 149, true},
		// Bits 20-25 = 010000 = 0x10 (two hex digits).
		{"0x01000000", "145.10", 145, true},
		{"", "", 149, false},
		{"not-hex", "", 149, false},
	}
	for _, tt := range tests {
		got, ok := nvlink5SubXidCode(tt.code, tt.intrInfo)
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("nvlink5SubXidCode(%d, %q) = (%q, %v), want (%q, %v)", tt.code, tt.intrInfo, got, ok, tt.want, tt.wantOK)
		}
	}
}
