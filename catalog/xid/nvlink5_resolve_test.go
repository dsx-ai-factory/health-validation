package xid_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// Values below are taken from captured NVRM output. Per NVIDIA's decode
// table, Xid 145 RLW_SRC_TRACK resolves by ErrorStatus: 0x00000008 defers
// to Xid 154, while 0x00000020 names RESET_GPU outright. Having one of
// each from the same subcomponent is what makes the deferral path
// testable without also exercising subcode matching.
const (
	deferToXid154 = "0x00000008"
	resetOutright = "0x00000020"
)

func srcTrackEvent(pci, errorStatus string) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: pci,
		XidCode:    145,
		NVLink5: &xid.NVLink5Detail{
			SubComponent: "RLW_SRC_TRACK",
			IntrInfo:     "0x040988e2",
			ErrorStatus:  errorStatus,
			Fatal:        true,
		},
	}
}

func TestNVLink5ResolutionCounts_TableRowNeedsNo154(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright),
	}, "575.51.03")

	if got[xid.ResolutionResetGPU] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, xid.ResolutionResetGPU)
	}
}

func TestNVLink5ResolutionCounts_DeferralUsesThe154(t *testing.T) {
	tests := []struct {
		name    string
		toCode  string
		toName  string
		wantKey string
	}{
		{"reset required", "0x1", "GPU Reset Required", xid.ResolutionResetGPU},
		{"drain and reset", "0x4", "Drain and Reset", xid.ResolutionResetGPU},
		{"none clears it", "0x0", "None", xid.ResolutionIgnore},
		{"unmapped code is surfaced", "0x9", "Some Future Action", xid.ResolutionXid154Unrecognized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
				srcTrackEvent("0019:01:00", deferToXid154),
				recoveryEvent("0019:01:00", "0x0", "None", tc.toCode, tc.toName),
			}, "575.51.03")

			if got[tc.wantKey] != 1 {
				t.Errorf("counts = %v, want %s -> 1", got, tc.wantKey)
			}
			if _, ok := got[xid.ResolutionXid154Eval]; ok {
				t.Errorf("counts = %v, XID_154_EVAL must never survive resolution", got)
			}
		})
	}
}

// TestNVLink5ResolutionCounts_DeferralWithNo154 is the below-R565 case:
// NVIDIA records no Xid 154 linkage for this family before CUDA 12.7 /
// driver R565, so the deferral has nothing to defer to. It must be
// visible rather than counted as "no action needed".
func TestNVLink5ResolutionCounts_DeferralWithNo154(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", deferToXid154),
	}, "560.35.03")

	if got[xid.ResolutionXid154Missing] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, xid.ResolutionXid154Missing)
	}
	if got[xid.ResolutionIgnore] != 0 {
		t.Errorf("counts = %v, a missing 154 must not read as IGNORE", got)
	}
}

// TestNVLink5ResolutionCounts_ResolvedPerGPU is the payoff of attributing
// recovery actions per PCI address. Both GPUs report the identical fault
// at the identical ErrorStatus in one window; only one escalates. Joining
// by window rather than by GPU would resolve both to RESET_GPU and
// recommend resetting a GPU the driver never asked to reset.
func TestNVLink5ResolutionCounts_ResolvedPerGPU(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0018:01:00", deferToXid154),
		srcTrackEvent("0019:01:00", deferToXid154),
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
	}, "575.51.03")

	if got[xid.ResolutionResetGPU] != 1 {
		t.Errorf("counts = %v, want exactly one %s (0019 only)", got, xid.ResolutionResetGPU)
	}
	if got[xid.ResolutionXid154Missing] != 1 {
		t.Errorf("counts = %v, want exactly one %s (0018 only)", got, xid.ResolutionXid154Missing)
	}
}

// TestNVLink5ResolutionCounts_CountsEventsNotGPUs pins the deliberate
// difference from recovery_action_counts, which counts GPUs: two faults
// on one GPU are two occurrences to investigate.
func TestNVLink5ResolutionCounts_CountsEventsNotGPUs(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright),
		srcTrackEvent("0019:01:00", resetOutright),
	}, "575.51.03")

	if got[xid.ResolutionResetGPU] != 2 {
		t.Errorf("counts = %v, want %s -> 2", got, xid.ResolutionResetGPU)
	}
}

func TestNVLink5ResolutionCounts_IgnoresNonNVLink5Events(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		{PCIAddress: "0019:01:00", XidCode: 79, ProcessName: "python3"},
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
	}, "575.51.03")

	if len(got) != 0 {
		t.Errorf("counts = %v, want empty: no NVLink5-family events present", got)
	}
}
