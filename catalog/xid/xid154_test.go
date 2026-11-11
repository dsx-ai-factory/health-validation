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

// hostCatalog builds the Xid catalog for a host running driverVersion,
// the way the app hands the version it discovers to the catalog.
func hostCatalog(t *testing.T, driverVersion string) *catalog.Catalog {
	t.Helper()
	v, ok := health.ParseDriverVersion(driverVersion)
	if !ok {
		t.Fatalf("ParseDriverVersion(%q) failed", driverVersion)
	}
	return xidCatalog(t, catalog.WithDriverVersion(v))
}

// hostCatalogWith is [hostCatalog] at a v2-era driver with extra options.
func hostCatalogWith(t *testing.T, opts ...catalog.Option) *catalog.Catalog {
	t.Helper()
	v, _ := health.ParseDriverVersion("575.51.03")
	return xidCatalog(t, append([]catalog.Option{catalog.WithDriverVersion(v)}, opts...)...)
}

// TestDerivedSignals_RecoveryActionKeysAreCanonical is the same problem
// one layer up: a padded rendering must not split one action across two
// signal keys, or a rule matching "0x1" misses a GPU that needs a reset.
func TestDerivedSignals_RecoveryActionKeysAreCanonical(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{
		recoveryEvent("0018:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
		recoveryEvent("0019:01:00", "0x0", "None", "0x00000001", "GPU Reset Required"),
	}}
	counts := m.DerivedSignals(health.Host{})[xid.SignalRecoveryActionCounts].(map[string]int64)

	if counts["0x1"] != 2 {
		t.Errorf("counts = %v, want both GPUs under 0x1", counts)
	}
	if len(counts) != 1 {
		t.Errorf("counts = %v, want one key: the two spellings are one action", counts)
	}
}

// TestDerivedSignals_UnparseableActionCodeSurvives pins the pass-through:
// a code that is not hex at all stays visible under whatever NVRM printed
// rather than vanishing from the signal.
func TestDerivedSignals_UnparseableActionCodeSurvives(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "not-hex", "Something Odd"),
	}}
	counts := m.DerivedSignals(health.Host{})[xid.SignalRecoveryActionCounts].(map[string]int64)

	if counts["not-hex"] != 1 {
		t.Errorf("counts = %v, want the raw value preserved", counts)
	}
}

// TestXid154Check_ReportsRequestedAction pins what gpu_xid_154 reports for
// each recovery action a GPU was left in. A 154 can accompany any Xid, so
// this is the only check that sees a request made alongside, say, an
// Xid 94.
func TestXid154Check_ReportsRequestedAction(t *testing.T) {
	restartOS := health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)
	reset := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)

	tests := []struct {
		name         string
		wantMessage  string
		toCodes      []string
		wantAction   health.RecommendedAction
		wantSeverity health.Severity
	}{
		{name: "gpu reset", toCodes: []string{"0x1"}, wantSeverity: health.Critical, wantAction: reset},
		{name: "drain and reset", toCodes: []string{"0x4"}, wantSeverity: health.Critical, wantAction: reset, wantMessage: "let unaffected jobs finish or reach a checkpoint, then reset the GPU"},
		{name: "os reboot", toCodes: []string{"0x2"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "system reboot", toCodes: []string{"0x7"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "bus reset", toCodes: []string{"0x6"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "drain p2p", toCodes: []string{"0x3"}, wantSeverity: health.Warning, wantAction: health.Drain(health.RemediationActionStopP2PTraffic, health.RemediationActionResetGPU, health.RemediationActionServiceRequired)},
		{name: "unmapped", toCodes: []string{"0x5"}, wantSeverity: health.Warning, wantAction: health.Monitor},
		{name: "reboot outranks reset", toCodes: []string{"0x1", "0x2"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "none", toCodes: []string{"0x0"}, wantSeverity: health.Info, wantAction: health.NoAction},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := xid.XidMeasurement{Since: "boot", XidCounts: map[int64]int64{94: 1}, EventCount: 1}
			m.Events = append(m.Events, xid.XidEvent{PCIAddress: "0018:01:00", XidCode: 94})
			for i, code := range tc.toCodes {
				m.Events = append(m.Events, recoveryEvent(fmt.Sprintf("00%d9:01:00", i), "0x0", "None", code, "Action"))
				m.XidCounts[154]++
				m.EventCount++
			}
			nv, err := hostCatalog(t, "575.51.03").EvaluateAll(m)
			if err != nil {
				t.Fatalf("EvaluateAll: %v", err)
			}
			v, ok := checkResult(nv, "gpu_xid_154")
			if !ok {
				t.Fatalf("gpu_xid_154 verdict not found")
			}
			if v.Severity != tc.wantSeverity {
				t.Errorf("severity = %v, want %v", v.Severity, tc.wantSeverity)
			}
			if !v.RecommendedAction.Equal(tc.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tc.wantAction)
			}
			if tc.wantMessage != "" && !strings.Contains(strings.Join(v.Messages, " "), tc.wantMessage) {
				t.Errorf("Messages = %q, want one containing %q", v.Messages, tc.wantMessage)
			}
		})
	}
}

// xid154Verdict evaluates gpu_xid_154 against events.
func xid154Verdict(t *testing.T, events ...xid.XidEvent) health.CheckVerdict {
	t.Helper()
	m := xid.XidMeasurement{Since: "boot", XidCounts: map[int64]int64{}, Events: events, EventCount: int64(len(events))}
	for _, ev := range events {
		m.XidCounts[ev.XidCode]++
	}
	nv, err := hostCatalog(t, "575.51.03").EvaluateAll(m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_154")
	if !ok {
		t.Fatalf("gpu_xid_154 verdict not found")
	}
	return v
}

// TestXid154Check_FollowsJournalOrder reverses the order events reach the
// catalog: the journal position, not delivery order, decides whether a
// None cleared a reboot request.
func TestXid154Check_FollowsJournalOrder(t *testing.T) {
	reboot := recoveryEvent("0019:01:00", "0x0", "None", "0x2", "Node Reboot Required")
	none := recoveryEvent("0019:01:00", "0x2", "Node Reboot Required", "0x0", "None")

	// None, then Node Reboot, delivered in reverse.
	n, r := none, reboot
	n.Sequence, r.Sequence = 1, 2
	if v := xid154Verdict(t, r, n); v.Severity != health.Critical {
		t.Errorf("None then Node Reboot: severity = %v, want CRITICAL", v.Severity)
	}

	// Node Reboot, then None, delivered in reverse.
	n, r = none, reboot
	r.Sequence, n.Sequence = 1, 2
	if v := xid154Verdict(t, n, r); v.Severity != health.Info {
		t.Errorf("Node Reboot then None: severity = %v, want INFO", v.Severity)
	}

	// No journal order at all: the None cannot clear the request.
	if v := xid154Verdict(t, reboot, none); v.Severity != health.Critical {
		t.Errorf("unordered: severity = %v, want CRITICAL", v.Severity)
	}
}

// A per-GPU measurement's events are all that GPU's, so a later None
// clears an earlier request even when the two lines spell the address
// differently.
func TestXid154Check_OneGPUAnySpelling(t *testing.T) {
	reset := recoveryEvent("0000:89:00", "0x0", "None", "0x1", "GPU Reset Required")
	none := recoveryEvent("00000000:89:00", "0x1", "GPU Reset Required", "0x0", "None")
	reset.Sequence, none.Sequence = 1, 2
	m := xid.XidMeasurement{
		Since: "boot", GPUID: "GPU-a", PCIAddress: "0000:89:00",
		XidCounts: map[int64]int64{154: 2}, Events: []xid.XidEvent{reset, none}, EventCount: 2,
	}
	nv, err := hostCatalog(t, "575.51.03").EvaluateAll(m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_154")
	if !ok {
		t.Fatalf("gpu_xid_154 verdict not found")
	}
	if v.Severity != health.Info {
		t.Errorf("severity = %v %q, want INFO: the None came last", v.Severity, v.Messages)
	}
}

// TestXid154Check_UnreadableTransition covers an Xid 154 whose transition
// could not be read: alone or next to another Xid it is a WARNING to
// monitor, and it never replaces a request read for the same GPU.
func TestXid154Check_UnreadableTransition(t *testing.T) {
	unreadable := xid.XidEvent{PCIAddress: "0019:01:00", XidCode: 154}
	reset := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	for _, tc := range []struct {
		name         string
		events       []xid.XidEvent
		wantAction   health.RecommendedAction
		wantSeverity health.Severity
	}{
		{name: "alone", events: []xid.XidEvent{unreadable}, wantSeverity: health.Warning, wantAction: health.Monitor},
		{
			name:         "next to another Xid",
			events:       []xid.XidEvent{{PCIAddress: "0019:01:00", XidCode: 94}, unreadable},
			wantSeverity: health.Warning, wantAction: health.Monitor,
		},
		{
			name: "after a readable request on the same GPU",
			events: inJournalOrder(
				recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
				unreadable,
			),
			wantSeverity: health.Critical, wantAction: reset,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := xid154Verdict(t, tc.events...)
			if v.Severity != tc.wantSeverity {
				t.Errorf("severity = %v, want %v", v.Severity, tc.wantSeverity)
			}
			if !v.RecommendedAction.Equal(tc.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tc.wantAction)
			}
		})
	}
}

// TestXid154Check_OccurrencesPerAction pins that occurrences gates each
// action on the reports asking for it, so other GPUs' Xid 154 lines and
// actions a GPU has left cannot carry a request over the threshold.
func TestXid154Check_OccurrencesPerAction(t *testing.T) {
	cat := hostCatalogWith(t, occurrencesOverride("gpu_xid_154", 2))
	reset := recoveryEvent("0018:01:00", "0x0", "None", "0x1", "GPU Reset Required")
	for _, tc := range []struct {
		name   string
		events []xid.XidEvent
		want   health.Severity
	}{
		{
			name: "one request, other GPU's lines do not count",
			want: health.Pass,
			events: inJournalOrder(
				reset,
				recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
				recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x0", "None"),
			),
		},
		{
			name: "requested twice",
			want: health.Critical,
			events: inJournalOrder(
				reset,
				recoveryEvent("0018:01:00", "0x1", "GPU Reset Required", "0x0", "None"),
				reset,
			),
		},
		{
			name:   "two GPUs requesting",
			want:   health.Critical,
			events: inJournalOrder(reset, recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := xid.XidMeasurement{Since: "boot", XidCounts: map[int64]int64{154: int64(len(tc.events))}, Events: tc.events, EventCount: int64(len(tc.events))}
			nv, err := cat.EvaluateAll(m)
			if err != nil {
				t.Fatalf("EvaluateAll: %v", err)
			}
			v, ok := checkResult(nv, "gpu_xid_154")
			if !ok {
				t.Fatalf("gpu_xid_154 verdict not found")
			}
			if v.Severity != tc.want {
				t.Errorf("severity = %v, want %v", v.Severity, tc.want)
			}
		})
	}
}
