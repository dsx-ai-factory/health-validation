package xid_test

import (
	"testing"

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
	got := xid.TerminalRecoveryActions([]xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x1", "GPU Reset Required"),
	})

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
	got := xid.TerminalRecoveryActions([]xid.XidEvent{
		recoveryEvent("0018:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
		recoveryEvent("0018:01:00", "0x4", "Drain and Reset", "0x1", "GPU Reset Required"),
	})

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
	got := xid.TerminalRecoveryActions([]xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x0", "None"),
	})
	if got["0019:01:00"].ToName != "None" {
		t.Errorf("To = %q, want None", got["0019:01:00"].ToName)
	}
}

// TestDerivedSignals_KeySetIsDataIndependent pins the contract
// [health.DerivedSignaler] documents: routing asks a zero value which
// signals a type carries, so a key that only appeared once events were
// present would be invisible to it and any check requiring it would
// never be routed a measurement.
func TestDerivedSignals_KeySetIsDataIndependent(t *testing.T) {
	empty := xid.XidMeasurement{}.DerivedSignals(health.Host{})
	populated := xid.XidMeasurement{Events: []xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
	}}.DerivedSignals(health.Host{})

	if len(empty) != len(populated) {
		t.Fatalf("key counts differ: empty=%v populated=%v", empty, populated)
	}
	for key := range populated {
		if _, ok := empty[key]; !ok {
			t.Errorf("key %q appears only when data is present", key)
		}
	}

	// The consequence that actually matters: routing derives a type's
	// signal set from a zero value, so the derived name has to be
	// visible there or no check requiring it is ever routed.
	var routed bool
	for _, key := range evaluation.SchemaKeys(xid.XidMeasurement{}) {
		if key == xid.SignalRecoveryActionCounts {
			routed = true
		}
	}
	if !routed {
		t.Errorf("%s is not in a zero measurement's schema; checks requiring it would never be routed",
			xid.SignalRecoveryActionCounts)
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
// contract: a derived key equal to a json field name would silently
// replace that field rather than failing.
func TestDerivedSignals_DoNotShadowFields(t *testing.T) {
	derived := xid.XidMeasurement{}.DerivedSignals(health.Host{})
	for _, field := range evaluation.SchemaKeys(xid.XidMeasurement{}) {
		if _, clash := derived[field]; clash {
			// SchemaKeys includes derived names, so only a name that is
			// *also* a struct field is a real collision.
			var hasField bool
			for _, tag := range structJSONTags() {
				if tag == field {
					hasField = true
				}
			}
			if hasField {
				t.Errorf("derived signal %q shadows a struct field of the same name", field)
			}
		}
	}
}

// structJSONTags lists XidMeasurement's own json field names, without
// the derived ones SchemaKeys folds in.
func structJSONTags() []string {
	return []string{
		xid.SignalXidCounts,
		xid.SignalFatalXidCounts,
		xid.SignalSince,
		xid.SignalEvents,
		xid.SignalEventCount,
	}
}

// TestDerivedSignals_CountsGPUsNotLogLines is why the signal routes
// through TerminalRecoveryActions instead of counting 154 events: one
// escalating GPU logs several lines but needs one reset.
func TestDerivedSignals_CountsGPUsNotLogLines(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x4", "Drain and Reset"),
		recoveryEvent("0019:01:00", "0x4", "Drain and Reset", "0x1", "GPU Reset Required"),
	}}
	counts := m.DerivedSignals(health.Host{})[xid.SignalRecoveryActionCounts].(map[string]int64)

	if counts["0x1"] != 1 {
		t.Errorf("terminal action count = %v, want 0x1 -> 1", counts)
	}
	if _, ok := counts["0x4"]; ok {
		t.Errorf("counts = %v, want no entry for the superseded 0x4", counts)
	}
}
