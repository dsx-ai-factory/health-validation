package xid

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

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

func TestXidEvent_SubXidCode(t *testing.T) {
	tests := []struct {
		intrInfo string
		want     string
		code     int64
		wantOK   bool
	}{
		// Bits 25:20 = 000100 = 4. Taken from captured NVRM output.
		{"0x004505c6", "149.4", 149, true},
		{"0x00000091", "149.0", 149, true},
		{"0x00A00000", "149.A", 149, true},
		// Two hex digits once the field exceeds 0xF.
		{"0x01000000", "145.10", 145, true},
		{"", "", 149, false},
		{"not-hex", "", 149, false},
	}
	for _, tt := range tests {
		ev := XidEvent{XidCode: tt.code, NVLink5: &NVLink5Detail{IntrInfo: tt.intrInfo}}
		got, ok := ev.SubXidCode()
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("SubXidCode(%d, %q) = (%q, %v), want (%q, %v)", tt.code, tt.intrInfo, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestXidEvent_SubXidCode_NotNVLink5(t *testing.T) {
	if got, ok := (XidEvent{XidCode: 79}).SubXidCode(); ok {
		t.Errorf("SubXidCode on a non-NVLink5 event = (%q, true), want ok=false", got)
	}
}

// TestSubXidCode_AgreesWithDecodeTable pins that the key a live event
// produces is the key the per-subcode checks are derived under. The
// checks read the digit from each row's IntrInfo pattern; events read
// it through a register mask. If the two disagreed on bit position or
// formatting, every per-subcode check would silently never fire.
//
// Built from both driver eras' patterns, so it also pins that NVIDIA
// keeps IntrInfo[25:20] in the same place across the R575 change —
// the reason SubXidCode needs no driver version.
func TestSubXidCode_AgreesWithDecodeTable(t *testing.T) {
	var checked int
	for _, row := range nvlink5DecodeTable {
		digit, ok := nvlink5SubXidCodeDigit(row)
		if !ok {
			continue
		}
		want := fmt.Sprintf("%d.%s", row.code, digit)
		for era, pattern := range map[string]string{"v1": row.v1Pattern, "v2": row.v2Pattern} {
			v, err := strconv.ParseUint(strings.ReplaceAll(pattern, "-", "0"), 2, 32)
			if err != nil {
				t.Fatalf("%s %s: unparseable %s pattern %q", want, row.subcode, era, pattern)
			}
			ev := XidEvent{XidCode: row.code, NVLink5: &NVLink5Detail{IntrInfo: fmt.Sprintf("0x%08x", v)}}
			got, ok := ev.SubXidCode()
			if !ok || got != want {
				t.Errorf("%s row %s (%s pattern): event keys as %q, check expects %q", row.subcode, want, era, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no decode-table row yielded a digit; the test checked nothing")
	}
}

func TestSubXidCodeCounts(t *testing.T) {
	got := subXidCodeCounts([]XidEvent{
		{XidCode: 149, NVLink5: &NVLink5Detail{IntrInfo: "0x004505c6"}},
		{XidCode: 149, NVLink5: &NVLink5Detail{IntrInfo: "0x00400000"}},
		{XidCode: 149, NVLink5: &NVLink5Detail{IntrInfo: "0x00A00000"}},
		// Events with nothing to key on contribute nothing.
		{XidCode: 79, ProcessName: "python3"},
		{XidCode: 149, NVLink5: &NVLink5Detail{IntrInfo: ""}},
	})
	want := map[string]int64{"149.4": 2, "149.A": 1}
	if len(got) != len(want) {
		t.Fatalf("subXidCodeCounts = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("subXidCodeCounts[%q] = %d, want %d", k, got[k], n)
		}
	}
}
