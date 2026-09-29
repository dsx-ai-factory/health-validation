package xid_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit/xid"
)

func TestParseXid_FullLine(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	if m.Since != "boot" {
		t.Errorf("Since = %q, want boot", m.Since)
	}
	if got := m.XidCounts[79]; got != 1 {
		t.Errorf("XidCounts[79] = %d, want 1", got)
	}

	ev := m.Events[0]
	if ev.XidCode != 79 {
		t.Errorf("XidCode = %d, want 79", ev.XidCode)
	}
	if ev.Pid != 1234 {
		t.Errorf("Pid = %d, want 1234", ev.Pid)
	}
	if ev.ProcessName != "python3" {
		t.Errorf("ProcessName = %q, want python3", ev.ProcessName)
	}
	if ev.PCIAddress != "0000:01:00" {
		t.Errorf("PCIAddress = %q, want 0000:01:00", ev.PCIAddress)
	}
}

func TestParseXid_CodeOnly(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 31, Ch 0000000a, intr 10000000",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	ev := m.Events[0]
	if ev.XidCode != 31 {
		t.Errorf("XidCode = %d, want 31", ev.XidCode)
	}
	if ev.Pid != 0 {
		t.Errorf("Pid = %d, want 0", ev.Pid)
	}
	if ev.ProcessName != "" {
		t.Errorf("ProcessName = %q, want empty", ev.ProcessName)
	}
}

func TestParseXid_RepeatedCodeIncrementsCount(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1, name=a",
		"NVRM: Xid (PCI:0000:02:00): 79, pid=2, name=b",
		"NVRM: Xid (PCI:0000:03:00): 31, pid=3, name=c",
	})

	if m.EventCount != 3 {
		t.Fatalf("EventCount = %d, want 3", m.EventCount)
	}
	if got := m.XidCounts[79]; got != 2 {
		t.Errorf("XidCounts[79] = %d, want 2", got)
	}
	if got := m.XidCounts[31]; got != 1 {
		t.Errorf("XidCounts[31] = %d, want 1", got)
	}
	if len(m.Events) != 3 {
		t.Errorf("len(Events) = %d, want 3", len(m.Events))
	}
}

func TestParseXid_WithJournalPrefix(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"Jun 09 12:00:00 node1 kernel: NVRM: Xid (PCI:0000:65:00): 48, pid=999, name=cuda-app",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	ev := m.Events[0]
	if ev.XidCode != 48 || ev.ProcessName != "cuda-app" || ev.PCIAddress != "0000:65:00" {
		t.Errorf("parsed %+v, want code=48 name=cuda-app pci=0000:65:00", ev)
	}
}

func TestParseXid_NonMatchingLinesSkipped(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"",
		"NVRM: GPU at 0000:01:00.0: GPU has fallen off the bus.",
		"some unrelated kernel message",
		"NVRM: Xid (PCI:0000:01:00): 13, pid=42, name=worker",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1 (only the Xid line)", m.EventCount)
	}
	if m.Events[0].XidCode != 13 {
		t.Errorf("XidCode = %d, want 13", m.Events[0].XidCode)
	}
}

func TestParseXid_NVLink5Fatal(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000 0x00000001 0x00000002)",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	if got := m.XidCounts[149]; got != 1 {
		t.Errorf("XidCounts[149] = %d, want 1", got)
	}
	if got := m.FatalXidCounts[149]; got != 1 {
		t.Errorf("FatalXidCounts[149] = %d, want 1", got)
	}

	ev := m.Events[0]
	if ev.XidCode != 149 {
		t.Errorf("XidCode = %d, want 149", ev.XidCode)
	}
	if ev.PCIAddress != "0018:01:00" {
		t.Errorf("PCIAddress = %q, want 0018:01:00", ev.PCIAddress)
	}
	if ev.NVLink5 == nil {
		t.Fatalf("NVLink5 = nil, want populated")
	}
	if ev.NVLink5.SubComponent != "NETIR_LINK_EVT" {
		t.Errorf("SubComponent = %q, want NETIR_LINK_EVT", ev.NVLink5.SubComponent)
	}
	if !ev.NVLink5.Fatal {
		t.Errorf("Fatal = false, want true")
	}
	if ev.NVLink5.Link != 8 {
		t.Errorf("Link = %d, want 8", ev.NVLink5.Link)
	}
	if ev.NVLink5.IntrInfo != "0x004505c6" {
		t.Errorf("IntrInfo = %q, want 0x004505c6", ev.NVLink5.IntrInfo)
	}
	if ev.NVLink5.ErrorStatus != "0x00000000" {
		t.Errorf("ErrorStatus = %q, want 0x00000000", ev.NVLink5.ErrorStatus)
	}
	if want := []string{"0x00000001", "0x00000002"}; len(ev.NVLink5.DebugData) != len(want) || ev.NVLink5.DebugData[0] != want[0] || ev.NVLink5.DebugData[1] != want[1] {
		t.Errorf("DebugData = %v, want %v", ev.NVLink5.DebugData, want)
	}
}

func TestParseXid_NVLink5Nonfatal(t *testing.T) {
	m := xid.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02",
	})

	if m.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", m.EventCount)
	}
	if got := m.XidCounts[145]; got != 1 {
		t.Errorf("XidCounts[145] = %d, want 1", got)
	}
	if len(m.FatalXidCounts) != 0 {
		t.Errorf("FatalXidCounts = %v, want empty (Nonfatal event)", m.FatalXidCounts)
	}

	ev := m.Events[0]
	if ev.NVLink5 == nil {
		t.Fatalf("NVLink5 = nil, want populated")
	}
	if ev.NVLink5.SubComponent != "RLW_REMAP" {
		t.Errorf("SubComponent = %q, want RLW_REMAP", ev.NVLink5.SubComponent)
	}
	if ev.NVLink5.Fatal {
		t.Errorf("Fatal = true, want false")
	}
	if ev.NVLink5.Link != 2 {
		t.Errorf("Link = %d, want 2", ev.NVLink5.Link)
	}
	// No parenthesized register data on this line — must not be a parse error.
	if ev.NVLink5.IntrInfo != "" {
		t.Errorf("IntrInfo = %q, want empty (no register data on this line)", ev.NVLink5.IntrInfo)
	}
}

func TestParseXid_NonNVLink5CodesUnaffected(t *testing.T) {
	// A regular (non-144-150) Xid line must still parse via the
	// pid=/name= format and never populate NVLink5 or FatalXidCounts.
	m := xid.ParseXid("boot", []string{
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
	})
	if m.Events[0].NVLink5 != nil {
		t.Errorf("NVLink5 = %+v, want nil for a non-144-150 code", m.Events[0].NVLink5)
	}
	if len(m.FatalXidCounts) != 0 {
		t.Errorf("FatalXidCounts = %v, want empty", m.FatalXidCounts)
	}
}

func TestParseXid_NoLines_EmptyMeasurement(t *testing.T) {
	m := xid.ParseXid("boot", nil)

	if m.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0", m.EventCount)
	}
	if len(m.Events) != 0 {
		t.Errorf("len(Events) = %d, want 0", len(m.Events))
	}
	if m.Category() != health.Xid {
		t.Errorf("Category = %v, want Xid", m.Category())
	}
}

// TestParseXid_NVLink5_RealWorldVariations pins the log-format
// variations that appear in captured NVRM output but not in the
// idealized form. Each of these once fell through to the generic
// pid=/name= parser, which silently produced an event with no
// NVLink5 detail at all — no sub-component, no Fatal flag, and none of
// the registers the catalog decodes a resolution from. A regression
// here is invisible in XidCounts, so assert on the detail.
func TestParseXid_NVLink5_RealWorldVariations(t *testing.T) {
	tests := []struct {
		name             string
		line             string
		wantSubComponent string
		wantIntrInfo     string
		wantErrorStatus  string
		wantProcessName  string
		wantPid          int64
		wantLink         int64
		wantFatal        bool
	}{
		{
			name:             "pid and name precede the sub-component",
			line:             "NVRM: Xid (PCI:0009:01:00): 145, pid=2348952, name=cache_mgr_main, RLW_SRC_TRACK Fatal XC1 i0 Link 17 (0x040a28e2 0x00000020 0x00000000)",
			wantSubComponent: "RLW_SRC_TRACK",
			wantIntrInfo:     "0x040a28e2",
			wantErrorStatus:  "0x00000020",
			wantPid:          2348952,
			wantProcessName:  "cache_mgr_main",
			wantLink:         17,
			wantFatal:        true,
		},
		{
			name:             "runs of whitespace between fields",
			line:             "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT  Fatal   XC0 i0 Link 17 (0x004001C6 0x00000080 0x00000000)",
			wantSubComponent: "NETIR_LINK_EVT",
			wantIntrInfo:     "0x004001C6",
			wantErrorStatus:  "0x00000080",
			wantLink:         17,
			wantFatal:        true,
		},
		{
			name:             "link -1 when not attributable to one link",
			line:             "NVRM: Xid (PCI:0009:01:00): 149, NETIR  Fatal   XC0 i0 Link -1 (0x000fe406 0x00000000 0x00000000)",
			wantSubComponent: "NETIR",
			wantIntrInfo:     "0x000fe406",
			wantErrorStatus:  "0x00000000",
			wantLink:         -1,
			wantFatal:        true,
		},
		{
			name:             "nonfatal with no register tail",
			line:             "NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02",
			wantSubComponent: "RLW_REMAP",
			wantLink:         2,
			wantFatal:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := xid.ParseXid("boot", []string{tc.line})
			if m.EventCount != 1 {
				t.Fatalf("EventCount = %d, want 1", m.EventCount)
			}
			ev := m.Events[0]
			if ev.NVLink5 == nil {
				t.Fatalf("NVLink5 = nil, want populated (line fell through to the generic parser)")
			}
			if ev.NVLink5.SubComponent != tc.wantSubComponent {
				t.Errorf("SubComponent = %q, want %q", ev.NVLink5.SubComponent, tc.wantSubComponent)
			}
			if ev.NVLink5.IntrInfo != tc.wantIntrInfo {
				t.Errorf("IntrInfo = %q, want %q", ev.NVLink5.IntrInfo, tc.wantIntrInfo)
			}
			if ev.NVLink5.ErrorStatus != tc.wantErrorStatus {
				t.Errorf("ErrorStatus = %q, want %q", ev.NVLink5.ErrorStatus, tc.wantErrorStatus)
			}
			if ev.NVLink5.Link != tc.wantLink {
				t.Errorf("Link = %d, want %d", ev.NVLink5.Link, tc.wantLink)
			}
			if ev.NVLink5.Fatal != tc.wantFatal {
				t.Errorf("Fatal = %v, want %v", ev.NVLink5.Fatal, tc.wantFatal)
			}
			if ev.Pid != tc.wantPid {
				t.Errorf("Pid = %d, want %d", ev.Pid, tc.wantPid)
			}
			if ev.ProcessName != tc.wantProcessName {
				t.Errorf("ProcessName = %q, want %q", ev.ProcessName, tc.wantProcessName)
			}
		})
	}
}
