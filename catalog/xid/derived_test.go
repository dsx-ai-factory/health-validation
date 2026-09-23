package xid_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// TestXidMeasurement_DerivedSignalContract supplies the populated
// measurement the shared contract checkers need; only this package
// knows what exercises the Xid derivations.
func TestXidMeasurement_DerivedSignalContract(t *testing.T) {
	populated := xid.XidMeasurement{Events: []xid.XidEvent{
		{XidCode: 149, NVLink5: &xid.NVLink5Detail{IntrInfo: "0x004505c6"}},
	}}
	if err := catalog.CheckDerivedSignalKeysStable(populated); err != nil {
		t.Error(err)
	}
	if err := catalog.CheckDerivedSignals(xid.XidMeasurement{}); err != nil {
		t.Error(err)
	}
}

// TestSubXidCodeCounts_ReachesCEL is the end-to-end claim: a sensor that
// supplies only events, with no counts of its own, still gets
// sub_xid_code_counts in front of the rules.
func TestSubXidCodeCounts_ReachesCEL(t *testing.T) {
	m := xid.XidMeasurement{Events: []xid.XidEvent{
		{XidCode: 149, NVLink5: &xid.NVLink5Detail{IntrInfo: "0x004505c6"}},
	}}
	counts, ok := evaluation.ToSignals(m)[xid.SignalSubXidCodeCounts].(map[string]int64)
	if !ok {
		t.Fatalf("%s missing or wrong type in CEL signals", xid.SignalSubXidCodeCounts)
	}
	if counts["149.4"] != 1 {
		t.Errorf("counts = %v, want 149.4 -> 1", counts)
	}
}
