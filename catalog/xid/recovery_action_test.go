// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"maps"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func nvlink5Event(pci string, code int64, subComponent, errorStatus string) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: pci,
		XidCode:    code,
		NVLink5: &xid.NVLink5Detail{
			SubComponent: subComponent,
			ErrorStatus:  errorStatus,
		},
	}
}

// inJournalOrder numbers events by their position, as a journal scan
// does, so order-dependent derivations see them as logged.
func inJournalOrder(events ...xid.XidEvent) []xid.XidEvent {
	for i := range events {
		events[i].Sequence = int64(i + 1)
	}
	return events
}

func recoveryEvent(pci, fromCode, fromName, toCode, toName string) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: pci,
		XidCode:    154,
		RecoveryAction: &xid.RecoveryAction{
			FromCode: fromCode,
			FromName: fromName,
			ToCode:   toCode,
			ToName:   toName,
		},
	}
}

// TestTerminalRecoveryActions_EscalationChain covers the shape captured
// logs actually produce: a GPU escalates through several transitions in
// one window, and only the last one describes the action in effect.
func TestTerminalRecoveryActions_EscalationChain(t *testing.T) {
	got := xid.TerminalRecoveryActions(inJournalOrder(
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x1", "GPU Reset Required"),
	))

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	action, ok := got["0019:01:00"]
	if !ok {
		t.Fatalf("no action for 0019:01:00: %+v", got)
	}
	if action.ToCode != "0x1" || action.ToName != "GPU Reset Required" {
		t.Errorf("To = %s (%s), want 0x1 (GPU Reset Required)", action.ToCode, action.ToName)
	}
}

// TestTerminalRecoveryActions_AttributedPerGPU is the case that makes
// per-PCI attribution load-bearing rather than incidental. Both GPUs
// report the same Xid 145 subtype at the same ErrorStatus in one
// window, and only one of them escalates. Attributing by window rather
// than by GPU would recommend a reset for a GPU the driver never asked
// to reset.
func TestTerminalRecoveryActions_AttributedPerGPU(t *testing.T) {
	got := xid.TerminalRecoveryActions([]xid.XidEvent{
		nvlink5Event("0018:01:00", 145, "RLW_SRC_TRACK", "0x00000008"),
		nvlink5Event("0019:01:00", 145, "RLW_SRC_TRACK", "0x00000008"),
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
	})

	if _, ok := got["0018:01:00"]; ok {
		t.Errorf("0018:01:00 has an action, want none: %+v", got)
	}
	action, ok := got["0019:01:00"]
	if !ok {
		t.Fatalf("no action for 0019:01:00: %+v", got)
	}
	if action.ToName != "Drain and Reset" {
		t.Errorf("To = %q, want Drain and Reset", action.ToName)
	}
}

func TestTerminalRecoveryActions_IndependentPerGPU(t *testing.T) {
	got := xid.TerminalRecoveryActions(inJournalOrder(
		recoveryEvent("0018:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
		recoveryEvent("0018:01:00", "0x4", "Drain and Reset", "0x1", "GPU Reset Required"),
	))

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	// 0018's later transition must not be shadowed by 0019's.
	if got["0018:01:00"].ToCode != "0x1" {
		t.Errorf("0018:01:00 To = %q, want 0x1", got["0018:01:00"].ToCode)
	}
	if got["0019:01:00"].ToCode != "0x1" {
		t.Errorf("0019:01:00 To = %q, want 0x1", got["0019:01:00"].ToCode)
	}
}

func TestTerminalRecoveryActions_NoneReported(t *testing.T) {
	if got := xid.TerminalRecoveryActions(nil); got != nil {
		t.Errorf("nil events: got %+v, want nil", got)
	}
	// Faults with no accompanying 154 — the below-R565 case, where the
	// linkage does not exist at all.
	got := xid.TerminalRecoveryActions([]xid.XidEvent{
		nvlink5Event("0018:01:00", 145, "RLW_SRC_TRACK", "0x00000008"),
	})
	if got != nil {
		t.Errorf("no 154 events: got %+v, want nil", got)
	}
}

// TestTerminalRecoveryActions_RevertsToNone pins that a de-escalation is
// honoured rather than treated as a high-water mark: the driver can
// clear a recovery action, and the last transition still wins.
func TestTerminalRecoveryActions_RevertsToNone(t *testing.T) {
	got := xid.TerminalRecoveryActions(inJournalOrder(
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x0", "None"),
	))
	if got["0019:01:00"].ToName != "None" {
		t.Errorf("To = %q, want None", got["0019:01:00"].ToName)
	}
}

// TestTerminalRecoveryActions_OrderedBySequence pins that the journal
// position decides which transition is final, not the order a source
// handed the events over in.
func TestTerminalRecoveryActions_OrderedBySequence(t *testing.T) {
	reboot := recoveryEvent("0019:01:00", "0x0", "None", "0x2", "Node Reboot Required")
	none := recoveryEvent("0019:01:00", "0x2", "Node Reboot Required", "0x0", "None")
	for _, tc := range []struct {
		name   string
		events func() []xid.XidEvent
		want   string
	}{
		{name: "reboot logged last, delivered first", want: "0x2", events: func() []xid.XidEvent {
			r, n := reboot, none
			r.Sequence, n.Sequence = 2, 1
			return []xid.XidEvent{r, n}
		}},
		{name: "none logged last, delivered first", want: "0x0", events: func() []xid.XidEvent {
			r, n := reboot, none
			r.Sequence, n.Sequence = 1, 2
			return []xid.XidEvent{n, r}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := xid.TerminalRecoveryActions(tc.events())["0019:01:00"].ToCode; got != tc.want {
				t.Errorf("To = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTerminalRecoveryActions_UnknownOrderKeepsRequest covers reports
// whose order cannot be established: without a distinct Sequence each, a
// None must not clear a request that may have followed it.
func TestTerminalRecoveryActions_UnknownOrderKeepsRequest(t *testing.T) {
	reset := recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required")
	none := recoveryEvent("0019:01:00", "0x1", "GPU Reset Required", "0x0", "None")
	reboot := recoveryEvent("0019:01:00", "0x1", "GPU Reset Required", "0x2", "Node Reboot Required")
	tied := func(seq int64, evs ...xid.XidEvent) []xid.XidEvent {
		for i := range evs {
			evs[i].Sequence = seq
		}
		return evs
	}
	for _, tc := range []struct {
		name   string
		want   string
		events []xid.XidEvent
	}{
		{name: "no sequence", events: []xid.XidEvent{reset, none}, want: "0x1"},
		{name: "tied sequence", events: tied(7, reset, none), want: "0x1"},
		{name: "one report missing its sequence", events: append(tied(3, none), reset), want: "0x1"},
		{name: "most disruptive request wins", events: []xid.XidEvent{reset, reboot, none}, want: "0x2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := xid.TerminalRecoveryActions(tc.events)["0019:01:00"].ToCode; got != tc.want {
				t.Errorf("To = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDerivedSignals_KeySetIsDataIndependent supplies the populated
// measurement and host for the contract [health.DerivedSignaler]
// documents; the rule itself lives next to CheckSignalUniqueness, which
// is what reads a zero value's signal set. Only this package knows what
// "populated" looks like for an XidMeasurement.
func TestDerivedSignals_KeySetIsDataIndependent(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
		srcTrackEvent("0019:01:00", resetOutright),
	}}
	version, ok := health.ParseDriverVersion("575.51.03")
	if !ok {
		t.Fatal("ParseDriverVersion failed on a well-formed version")
	}
	host := health.Host{DriverVersion: version}
	if err := catalog.CheckDerivedSignalKeysStable(m, host); err != nil {
		t.Error(err)
	}
}

// TestDerivedSignals_ReachesCELSignals is the end-to-end check that the
// hook is wired up: a derived signal is worthless if ToSignals, which
// builds the CEL variable map, does not merge it.
func TestDerivedSignals_ReachesCELSignals(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
	}}
	signals := evaluation.ToSignals(m, health.Host{})

	counts, ok := signals[xid.SignalRecoveryActionCounts].(map[string]int64)
	if !ok {
		t.Fatalf("%s missing or wrong type in CEL signals: %#v",
			xid.SignalRecoveryActionCounts, signals[xid.SignalRecoveryActionCounts])
	}
	if counts["0x1"] != 1 {
		t.Errorf("counts = %v, want 0x1 -> 1", counts)
	}
}

// TestDerivedSignals_DoNotShadowFields covers the other half of the
// contract. CheckDerivedSignals reads the json field names by reflection,
// so unlike the hand-listed version this cannot go stale when a field is
// added to XidMeasurement.
func TestDerivedSignals_DoNotShadowFields(t *testing.T) {
	if err := catalog.CheckDerivedSignals(xid.XidMeasurement{}); err != nil {
		t.Error(err)
	}
}

// TestDerivedSignals_CountsGPUsNotLogLines is why the signal routes
// through TerminalRecoveryActions instead of counting 154 events: one
// escalating GPU logs several lines but needs one reset.
func TestDerivedSignals_CountsGPUsNotLogLines(t *testing.T) {
	m := xid.XidMeasurement{Events: inJournalOrder(
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x1", "GPU Reset Required"),
	)}
	counts := m.DerivedSignals(health.Host{})[xid.SignalRecoveryActionCounts].(map[string]int64)

	if counts["0x1"] != 1 {
		t.Errorf("terminal action count = %v, want 0x1 -> 1", counts)
	}
	if _, ok := counts["0x4"]; ok {
		t.Errorf("counts = %v, want no entry for the superseded 0x4", counts)
	}
}

// TestRecoveryActionCounts_CountsCurrentActionReports pins what each count
// means: reports asking for a GPU's current action, so actions a GPU has
// left drop out, and every report counts when its order is unknown.
func TestRecoveryActionCounts_CountsCurrentActionReports(t *testing.T) {
	counts := func(events ...xid.XidEvent) map[string]int64 {
		m := xid.XidMeasurement{Events: events}
		return m.DerivedSignals(health.Host{})[xid.SignalRecoveryActionCounts].(map[string]int64)
	}
	for _, tc := range []struct {
		name   string
		want   map[string]int64
		events []xid.XidEvent
	}{
		{
			name: "requested twice, cleared in between",
			want: map[string]int64{"0x1": 2},
			events: inJournalOrder(
				recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
				recoveryEvent("0019:01:00", "0x1", "GPU Reset Required", "0x0", "None"),
				recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
			),
		},
		{
			name: "one report per GPU",
			want: map[string]int64{"0x1": 2},
			events: inJournalOrder(
				recoveryEvent("0018:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
				recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
			),
		},
		{
			name: "unknown order counts every report",
			want: map[string]int64{"0x1": 1, "0x0": 1},
			events: []xid.XidEvent{
				recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
				recoveryEvent("0019:01:00", "0x1", "GPU Reset Required", "0x0", "None"),
			},
		},
		{
			name:   "every unreadable line",
			want:   map[string]int64{"unreadable": 2},
			events: []xid.XidEvent{{PCIAddress: "0019:01:00", XidCode: 154}, {PCIAddress: "0019:01:00", XidCode: 154}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := counts(tc.events...); !maps.Equal(got, tc.want) {
				t.Errorf("counts = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTerminalRecoveryActions_OnlyXid154 pins that a recovery action is
// read only from Xid 154 lines, even if another event carries one.
func TestTerminalRecoveryActions_OnlyXid154(t *testing.T) {
	other := recoveryEvent("0019:01:00", "0x0", "None", "0x2", "OS Reboot")
	other.XidCode = 13
	if got := xid.TerminalRecoveryActions([]xid.XidEvent{other}); got != nil {
		t.Errorf("TerminalRecoveryActions = %v, want nil for a non-154 event", got)
	}
}
