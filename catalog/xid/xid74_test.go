// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// xid74Event builds a journal-format Xid 74 event with a seven-register dump.
func xid74Event(gpu string, link int, registers [7]uint32) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: gpu, XidCode: 74,
		Message: fmt.Sprintf("NVRM: Xid (PCI:%s): 74, pid='<unknown>', name=<unknown>, NVLink: fatal error detected on link %d(0x%x, 0x%x, 0x%x, 0x%x, 0x%x, 0x%x, 0x%x)",
			gpu, link, registers[0], registers[1], registers[2], registers[3], registers[4], registers[5], registers[6]),
	}
}

// xid74Measurement builds a measurement from the supplied events.
func xid74Measurement(events ...xid.XidEvent) xid.XidMeasurement {
	return xid.XidMeasurement{Events: events}
}

// eval74 evaluates Xid 74 with the requested architecture and thresholds.
func eval74(t *testing.T, architecture string, m xid.XidMeasurement, opts ...catalog.Option) health.CheckVerdict {
	t.Helper()
	v, err := xidCatalogFor(t, architecture, opts...).Evaluate("gpu_xid_74", m)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// messages joins the diagnostic and recovery guidance for assertions.
func messages(v health.CheckVerdict) string { return strings.Join(v.Messages, " ") }

// TestXid74_HopperRegisterBits covers every documented Hopper bit and its default disposition.
func TestXid74_HopperRegisterBits(t *testing.T) {
	tests := []struct {
		message  string
		bits     []int
		register int
		severity health.Severity
	}{
		{"need no action", []int{0, 23, 30}, 0, health.Info},
		{"Secondary NVLink error", []int{1, 20}, 0, health.Critical},
		{"ECC/parity error", []int{4, 5}, 0, health.Critical},
		{"Possible NVLink hardware issue", []int{8, 9, 12, 16, 17, 24, 28}, 0, health.Critical},
		{"signal integrity", []int{21, 22}, 0, health.Critical},
		{"occurs repeatedly on the same link", []int{27, 29}, 0, health.Warning},
		{"ECC/parity error", []int{0, 1, 2, 6}, 2, health.Critical},
		{"not expected in production", []int{13}, 2, health.Critical},
		{"occurs repeatedly on the same link", []int{16, 19}, 2, health.Warning},
		{"occurs repeatedly on the same link", []int{17, 18}, 2, health.Warning},
		{"Secondary NVLink error", []int{16, 17}, 3, health.Critical},
		{"fabric reset", []int{18}, 3, health.Critical},
		{"ECC/parity error", []int{18, 19, 21, 22, 24, 25, 27, 28}, 4, health.Critical},
		{"no uncorrectable error occurred", []int{20, 23, 26, 29}, 4, health.Info},
	}
	for _, tt := range tests {
		for _, bit := range tt.bits {
			t.Run(fmt.Sprintf("register_%d_bit_%d", tt.register+1, bit), func(t *testing.T) {
				var registers [7]uint32
				registers[tt.register] = 1 << bit
				v := eval74(t, "H100", xid74Measurement(xid74Event("0000:01:00", 0, registers)))
				if v.Severity != tt.severity || !strings.Contains(messages(v), tt.message) {
					t.Fatalf("verdict = %+v, want %v with %q", v, tt.severity, tt.message)
				}
				want := health.GenericDrain()
				if tt.severity == health.Info {
					want = health.NoAction
				} else if tt.severity == health.Warning {
					want = health.Monitor
				} else if tt.message == "fabric reset" {
					want = health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)
				}
				if !v.RecommendedAction.Equal(want) {
					t.Errorf("action = %v, want %v", v.RecommendedAction, want)
				}
			})
		}
	}
}

// TestXid74_UnreadableAndUnmapped keeps incomplete or unknown register evidence visible.
func TestXid74_UnreadableAndUnmapped(t *testing.T) {
	valid := xid74Event("0000:01:00", 0, [7]uint32{1}).Message
	for name, message := range map[string]string{
		"missing":          "",
		"no register dump": "NVRM: Xid (PCI:0000:01:00): 74, NVLink: fatal error on link 2",
		"truncated":        strings.Replace(valid, ", 0x0)", ")", 1),
		"second tuple cut": valid + ", link 4(0x0, 0x0, 0x0, 0x40000",
		"second tuple neg": valid + ", link -4(0x0, 0x0, 0x0, 0x40000, 0x0, 0x0, 0x0)",
		"trailing header":  valid + ", link 4",
		"non-numeric link": valid + ", link x(0x0, 0x0, 0x0, 0x40000, 0x0, 0x0, 0x0)",
		"header no paren":  valid + ", link 4 0x40000",
		"extra register":   strings.Replace(valid, ")", " 0x0)", -1),
		"overflow":         strings.Replace(valid, "0x1", "0x100000000", 1),
		"empty field":      strings.Replace(valid, "0x1,", "0x1,,", 1),
		"negative link":    strings.Replace(valid, "link 0(", "link -1(", 1),
		"invalid hex":      strings.Replace(valid, "0x1", "0xGG", 1),
		"unknown bit":      xid74Event("0000:01:00", 0, [7]uint32{1 << 31}).Message,
		"unused register":  xid74Event("0000:01:00", 0, [7]uint32{1, 1}).Message,
		"zero registers":   xid74Event("0000:01:00", 0, [7]uint32{}).Message,
	} {
		t.Run(name, func(t *testing.T) {
			event := xid.XidEvent{XidCode: 74, PCIAddress: "0000:01:00", Message: message}
			if v := eval74(t, "H100", xid.XidMeasurement{Events: []xid.XidEvent{event}}); v.Severity != health.Warning || !v.RecommendedAction.Equal(health.Monitor) {
				t.Errorf("verdict = %+v, want WARNING/monitor", v)
			}
		})
	}
	for _, counts := range []map[int64]int64{{74: 1, 154: 1}, {74: 1}} {
		if v := eval74(t, "H100", xid.XidMeasurement{XidCounts: counts}); v.Severity != health.Warning || !v.RecommendedAction.Equal(health.Monitor) {
			t.Errorf("counts-only %v: verdict = %+v, want WARNING/monitor", counts, v)
		}
	}
}

// TestXid74_RecoveryDoesNotDependOn154 keeps recovery independent of companion reports.
func TestXid74_RecoveryDoesNotDependOn154(t *testing.T) {
	for _, fault := range []struct {
		name      string
		registers [7]uint32
		want      health.Severity
	}{
		{"mechanical", [7]uint32{1 << 8}, health.Critical},
		{"ECC once", [7]uint32{1 << 4}, health.Critical},
		{"ignored", [7]uint32{1}, health.Info},
		{"corrected ECC", [7]uint32{4: 1 << 20}, health.Info},
		{"ignored and corrected", [7]uint32{0: 1, 4: 1 << 20}, health.Info},
	} {
		for _, companion := range []struct {
			counts map[int64]int64
			name   string
			events []xid.XidEvent
		}{
			{name: "absent"},
			{name: "None", events: []xid.XidEvent{{XidCode: 154, PCIAddress: "0000:01:00", RecoveryAction: &xid.RecoveryAction{ToCode: "0x0"}}}},
			{name: "reset", events: []xid.XidEvent{{XidCode: 154, PCIAddress: "0000:01:00", RecoveryAction: &xid.RecoveryAction{ToCode: "0x1"}}}},
			{name: "other GPU", events: []xid.XidEvent{{XidCode: 154, PCIAddress: "0000:02:00", RecoveryAction: &xid.RecoveryAction{ToCode: "0x0"}}}},
			{name: "unreadable", events: []xid.XidEvent{{XidCode: 154, PCIAddress: "0000:01:00"}}},
			{name: "counts only", counts: map[int64]int64{74: 1, 154: 1}},
		} {
			t.Run(fault.name+"/"+companion.name, func(t *testing.T) {
				m := xid74Measurement(xid74Event("0000:01:00", 0, fault.registers))
				m.Events = append(m.Events, companion.events...)
				m.XidCounts = companion.counts
				v := eval74(t, "H100", m)
				wantAction := health.NoAction
				if fault.want == health.Critical {
					wantAction = health.GenericDrain()
				}
				if v.Severity != fault.want || !v.RecommendedAction.Equal(wantAction) {
					t.Fatalf("verdict = %+v, want %v with %v", v, fault.want, wantAction)
				}
			})
		}
	}
}

// TestXid74_ArchitectureAndThreshold separates Hopper decoding from A100 fallback and architecture skips.
func TestXid74_ArchitectureAndThreshold(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{xid74Event("0000:01:00", 0, [7]uint32{1})}}
	for _, arch := range []string{"H100", "H200"} {
		if v := eval74(t, arch, m); v.Severity != health.Info {
			t.Errorf("%s: %+v, want INFO", arch, v)
		}
	}
	if v := eval74(t, "A100", m); v.Severity != health.Critical || !v.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("A100: %+v, want CRITICAL with general recovery guidance", v)
	}
	for _, arch := range []string{"GB200", "GB300", "B200"} {
		if v := eval74(t, arch, m); v.SkipReason != health.ArchitectureUnsupported {
			t.Errorf("%s: %+v, want architecture skip", arch, v)
		}
	}
	if v := eval74(t, "H100", m, occurrencesOverride("gpu_xid_74", 2)); v.Severity != health.Pass {
		t.Errorf("below threshold: %+v, want PASS", v)
	}
	m.Events = append(m.Events, m.Events[0])
	if v := eval74(t, "H100", m, occurrencesOverride("gpu_xid_74", 2)); v.Severity != health.Info {
		t.Errorf("at threshold: %+v, want INFO", v)
	}
	for _, arch := range []string{"H100", "A100"} {
		if v := eval74(t, arch, xid.XidMeasurement{}, occurrencesOverride("gpu_xid_74", 0)); v.Severity != health.Pass {
			t.Errorf("%s empty: %+v, want PASS", arch, v)
		}
	}
}

// TestXid74_DecodeFlagCoversEveryArchitecture keeps a newly scoped architecture from defaulting silently.
func TestXid74_DecodeFlagCoversEveryArchitecture(t *testing.T) {
	for _, def := range xid.Checks() {
		if def.ID != "gpu_xid_74" {
			continue
		}
		spec := def.Thresholds["decode_hopper_registers"]
		for _, arch := range def.Architectures {
			if _, ok := spec.PerArchitecture[arch]; !ok {
				t.Errorf("decode_hopper_registers has no entry for %s", arch)
			}
		}
		return
	}
	t.Fatal("gpu_xid_74 not registered")
}

// TestXid74_MultipleLinksInOneMessage decodes every link tuple a line carries.
func TestXid74_MultipleLinksInOneMessage(t *testing.T) {
	const message = "NVRM: Xid (PCI:0000:01:00): 74, NVLink: fatal error detected on link 3(0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0), link 4(0x0, 0x0, 0x0, 0x40000, 0x0, 0x0, 0x0)"
	v := eval74(t, "H100", xid74Measurement(xid.XidEvent{XidCode: 74, PCIAddress: "0000:01:00", Message: message}))
	if v.Severity != health.Critical || !strings.Contains(messages(v), "fabric reset") {
		t.Errorf("verdict = %+v, want the second link's fabric reset", v)
	}
	counts := xid.XidMeasurement{Events: []xid.XidEvent{{XidCode: 74, Message: message}}}.DerivedSignals(health.Host{})[xid.SignalNVLinkErrorCounts].(map[string]int64)
	if counts["total"] != 1 || counts["ignored"] != 1 || counts["fabric_reset"] != 1 {
		t.Errorf("counts = %v, want one event with both links decoded", counts)
	}
}

// TestXid74_MixedBitsAndCheckScope keeps fabric recovery dominant and preserves scoped evidence.
func TestXid74_MixedBitsAndCheckScope(t *testing.T) {
	event := xid74Event("0000:01:00", 0, [7]uint32{1 | 1<<8, 0, 0, 1 << 18, 1 << 20})
	m := xid.XidMeasurement{Events: []xid.XidEvent{event, {XidCode: 13}}, XidCounts: map[int64]int64{74: 1, 13: 1}}
	nv, err := xidCatalogFor(t, "H100").EvaluateAll(m)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := checkResult(nv, "gpu_xid_74")
	if !ok || v.Severity != health.Critical {
		t.Fatalf("verdict = %+v, want CRITICAL", v)
	}
	if !v.RecommendedAction.Equal(health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)) {
		t.Errorf("action = %v, want fabric reset to outrank the link-hardware bit's GenericDrain", v.RecommendedAction)
	}
	events := v.ObservedValues[xid.SignalEvents].([]xid.XidEvent)
	if len(events) != 1 || events[0].Message != event.Message {
		t.Errorf("events = %+v, want only original Xid 74 message", events)
	}
	if unknownVerdict(t, nv).Severity != health.Pass || informationalVerdict(t, nv).Severity != health.Pass {
		t.Error("Xid 74 must not fire a catch-all check")
	}
}

// TestXid74_LogFormats preserves the fabric-reset bit across driver delimiters.
func TestXid74_LogFormats(t *testing.T) {
	const prefix = "NVRM: Xid (PCI:0000:01:00): 74, pid='<unknown>', name=<unknown>, "
	for _, message := range []string{
		"NVLink: fatal error detected on link 14(0x0, 0x0, 0x0, 0x40000, 0x0, 0x0, 0x0)",
		"NVLink: fatal error detected on link 14(0x0; 0x0; 0x0; 0x40000; 0x0; 0x0; 0x0)",
		"NVLINK: fatal error detected on link 14 (0x0 0x0 0x0 0x40000 0x0 0x0 0x0)",
		"NVLink: fatal error detected on link 14(0X0,0X0,0X0,0X40000,0X0,0X0,0X0)",
	} {
		t.Run(message, func(t *testing.T) {
			m := xid.XidMeasurement{Events: []xid.XidEvent{{XidCode: 74, Message: prefix + message}}}
			v := eval74(t, "H100", m)
			if v.Severity != health.Critical || !v.RecommendedAction.Equal(health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)) {
				t.Fatalf("verdict = %+v, want the fabric reset", v)
			}
		})
	}
}

// TestXid74_RecoveryWith154Disabled preserves reset guidance when the recovery check is disabled.
func TestXid74_RecoveryWith154Disabled(t *testing.T) {
	m := xid.XidMeasurement{PCIAddress: "0000:01:00", Events: []xid.XidEvent{
		xid74Event("0000:01:00", 0, [7]uint32{1 << 8}),
		{XidCode: 154, PCIAddress: "0000:01:00", RecoveryAction: &xid.RecoveryAction{ToCode: "0x1", ToName: "GPU Reset Required"}},
	}}
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled=%t", disabled), func(t *testing.T) {
			var options []catalog.Option
			if disabled {
				options = append(options, catalog.WithDisabledChecks("gpu_xid_154"))
			}
			nv, err := xidCatalogFor(t, "H100", options...).EvaluateAll(m)
			if err != nil {
				t.Fatal(err)
			}
			v, ok := checkResult(nv, "gpu_xid_74")
			if !ok || v.Severity != health.Critical || !v.RecommendedAction.Equal(health.GenericDrain()) || nv.OverallSeverity != health.Critical {
				t.Fatalf("Xid 74=%+v overall=%v, want independent CRITICAL/drain", v, nv.OverallSeverity)
			}
		})
	}
}

// TestXid74_SoloBits verifies that secondary and signal-integrity bits defer
// only to nonbenign, nondeferring groups in the same report.
func TestXid74_SoloBits(t *testing.T) {
	for _, tt := range []struct {
		name      string
		message   string
		registers [7]uint32
		want      health.Severity
	}{
		{"secondary alone", "without accompanying", [7]uint32{1 << 1}, health.Critical},
		{"secondary with ignorable bit", "without accompanying", [7]uint32{1<<1 | 1}, health.Critical},
		{"secondary with corrected ECC", "without accompanying", [7]uint32{0: 1 << 1, 4: 1 << 20}, health.Critical},
		{"secondary and signal integrity only", "without accompanying", [7]uint32{1<<1 | 1<<21}, health.Critical},
		{"secondary with link-hardware bit", "Secondary-error bits accompanied", [7]uint32{1<<1 | 1<<8}, health.Critical},
		{"secondary with one ECC report", "Secondary-error bits accompanied", [7]uint32{1<<1 | 1<<4}, health.Critical},
		{"register 4 secondary with ECC", "Secondary-error bits accompanied", [7]uint32{0: 1 << 4, 3: 1 << 16}, health.Critical},
		{"signal integrity with ECC", "Signal-integrity bits accompanied", [7]uint32{1<<21 | 1<<4}, health.Critical},
		{"secondary with recurring bit", "Secondary-error bits accompanied", [7]uint32{1<<1 | 1<<27}, health.Warning},
		{"secondary with undocumented bit", "without accompanying", [7]uint32{1<<1 | 1<<31}, health.Critical},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := eval74(t, "H100", xid74Measurement(xid74Event("0000:01:00", 0, tt.registers)))
			if v.Severity != tt.want || !strings.Contains(messages(v), tt.message) {
				t.Errorf("verdict = %+v, want %v with %q", v, tt.want, tt.message)
			}
		})
	}
	// An all-zero tuple on another link adds no error bits.
	const zero = "NVRM: Xid (PCI:0000:01:00): 74, NVLink: fatal error detected on link 3(0x2, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0), link 4(0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0)"
	if v := eval74(t, "H100", xid74Measurement(xid.XidEvent{XidCode: 74, PCIAddress: "0000:01:00", Message: zero})); v.Severity != health.Critical || !strings.Contains(messages(v), "without accompanying") {
		t.Errorf("secondary with an all-zero tuple: %+v, want CRITICAL", v)
	}
	// Bits on another link in the same report accompany the secondary bit too.
	const message = "NVRM: Xid (PCI:0000:01:00): 74, NVLink: fatal error detected on link 3(0x2, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0), link 4(0x10, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0)"
	if v := eval74(t, "H100", xid74Measurement(xid.XidEvent{XidCode: 74, PCIAddress: "0000:01:00", Message: message})); v.Severity != health.Critical || !strings.Contains(messages(v), "Secondary-error bits accompanied") {
		t.Errorf("secondary with ECC on another link: %+v, want CRITICAL from the ECC bit with the secondary bit accompanied", v)
	}
}

// TestXid74_EveryRuleHonorsOccurrences checks fabric-reset, unmapped and unreadable reports against occurrences.
func TestXid74_EveryRuleHonorsOccurrences(t *testing.T) {
	for name, event := range map[string]xid.XidEvent{
		"fabric reset": xid74Event("0000:01:00", 0, [7]uint32{3: 1 << 18}),
		"unmapped":     xid74Event("0000:01:00", 0, [7]uint32{1 << 31}),
		"unreadable":   {XidCode: 74, PCIAddress: "0000:01:00", Message: "NVRM: Xid (PCI:0000:01:00): 74, NVLink: fatal error on link 2"},
	} {
		t.Run(name, func(t *testing.T) {
			once := xid74Measurement(event)
			if v := eval74(t, "H100", once, occurrencesOverride("gpu_xid_74", 2)); v.Severity != health.Pass {
				t.Errorf("one report with occurrences=2: %+v, want PASS", v)
			}
			twice := xid74Measurement(event, event)
			if v := eval74(t, "H100", twice, occurrencesOverride("gpu_xid_74", 2)); v.Severity == health.Pass {
				t.Errorf("two reports with occurrences=2: %+v, want the rule to fire", v)
			}
		})
	}
}
