package xid_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
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

// resolutionInput is a measurement carrying NVLink5 events together with
// the driver version of the host it came from. The version is a fact the
// catalog is built with, not a field of the measurement.
type resolutionInput struct {
	driverVersion string
	m             xid.XidMeasurement
}

// resolutionMeasurement builds a measurement carrying NVLink5 events, so
// evaluation exercises the derived nvlink5_resolution_counts signal
// rather than a hand-set count.
func resolutionMeasurement(driverVersion string, events ...xid.XidEvent) resolutionInput {
	m := xid.XidMeasurement{Since: "boot", Events: events}
	m.XidCounts = map[int64]int64{}
	for _, ev := range events {
		m.XidCounts[ev.XidCode]++
		m.EventCount++
	}
	return resolutionInput{driverVersion: driverVersion, m: m}
}

func resolutionVerdict(t *testing.T, in resolutionInput) health.CheckVerdict {
	t.Helper()
	nv, err := hostCatalog(t, in.driverVersion).EvaluateAll(in.m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_nvlink5_resolution")
	if !ok {
		t.Fatalf("gpu_xid_nvlink5_resolution verdict not found")
	}
	return v
}

func TestNVLink5ResolutionCheck_ResetIsCritical(t *testing.T) {
	v := resolutionVerdict(t, resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", resetOutright)))

	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

// TestNVLink5ResolutionCheck_IgnoreIsPass is the reason this check exists.
// Roughly half the decode table's rows resolve to IGNORE; escalating them
// is exactly what reading the table is meant to avoid. The per-code
// gpu_xid_145 check still fires on the same event, which is the intended
// division of labour, not a conflict.
func TestNVLink5ResolutionCheck_IgnoreIsPass(t *testing.T) {
	ignored := xid.XidEvent{
		PCIAddress: "0019:01:00",
		XidCode:    145,
		NVLink5: &xid.NVLink5Detail{
			SubComponent: "RLW_SRC_TRACK",
			IntrInfo:     "0x040988e2",
			ErrorStatus:  "0x00000002",
		},
	}
	v := resolutionVerdict(t, resolutionMeasurement("575.51.03", ignored))

	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS (the table resolves this row to IGNORE)", v.Severity)
	}
}

func TestNVLink5ResolutionCheck_MissingXid154IsWarning(t *testing.T) {
	v := resolutionVerdict(t, resolutionMeasurement("560.35.03",
		srcTrackEvent("0019:01:00", deferToXid154)))

	if v.Severity != health.Warning {
		t.Errorf("Severity = %v, want WARNING", v.Severity)
	}
}

func TestNVLink5ResolutionCheck_UnrecognizedActionIsWarning(t *testing.T) {
	v := resolutionVerdict(t, resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", deferToXid154),
		recoveryEvent("0019:01:00", "0x0", "None", "0x9", "Some Future Action")))

	if v.Severity != health.Warning {
		t.Errorf("Severity = %v, want WARNING", v.Severity)
	}
}

func TestNVLink5ResolutionCheck_NoNVLink5EventsIsPass(t *testing.T) {
	v := resolutionVerdict(t, resolutionMeasurement("575.51.03"))

	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS", v.Severity)
	}
}

// unmatchedEvent uses a subcomponent NVIDIA's table does not cover, so
// decoding falls back to the Fatal flag rather than a confirmed row.
func unmatchedEvent(pci string, fatal bool) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: pci,
		XidCode:    146,
		NVLink5: &xid.NVLink5Detail{
			SubComponent: "TLW_NOT_IN_THE_TABLE",
			IntrInfo:     "0x04098000",
			ErrorStatus:  "0x00000040",
			Fatal:        fatal,
		},
	}
}

// TestNVLink5ResolutionCounts_UnmatchedFatalIsUnconfirmed separates a
// reset the table names from one the Fatal flag merely implies. Both
// escalate, but only one is documented guidance, and a verdict that
// cannot tell them apart overstates what NVIDIA actually said.
func TestNVLink5ResolutionCounts_UnmatchedFatalIsUnconfirmed(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		unmatchedEvent("0019:01:00", true),
	}, "575.51.03")

	if got[xid.ResolutionResetGPUUnconfirmed] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, xid.ResolutionResetGPUUnconfirmed)
	}
	if got[xid.ResolutionResetGPU] != 0 {
		t.Errorf("counts = %v, a fallback reset must not report as a table-confirmed %s",
			got, xid.ResolutionResetGPU)
	}
}

func TestNVLink5ResolutionCounts_UnmatchedNonfatalStaysIgnore(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		unmatchedEvent("0019:01:00", false),
	}, "575.51.03")

	if got[xid.ResolutionIgnore] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, xid.ResolutionIgnore)
	}
}

// TestNVLink5ResolutionCounts_MatchedResetStaysConfirmed is the other
// half of the distinction: a row the table does cover must not be
// downgraded to unconfirmed.
func TestNVLink5ResolutionCounts_MatchedResetStaysConfirmed(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright),
	}, "575.51.03")

	if got[xid.ResolutionResetGPU] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, xid.ResolutionResetGPU)
	}
	if _, ok := got[xid.ResolutionResetGPUUnconfirmed]; ok {
		t.Errorf("counts = %v, a table-confirmed row must not report as unconfirmed", got)
	}
}

func TestNVLink5ResolutionCheck_UnconfirmedResetIsCritical(t *testing.T) {
	v := resolutionVerdict(t, resolutionMeasurement("575.51.03",
		unmatchedEvent("0019:01:00", true)))

	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}
