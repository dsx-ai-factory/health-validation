package xid_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// Values below are taken from captured NVRM output. Per NVIDIA's decode
// table, Xid 145 RLW_SRC_TRACK resolves by ErrorStatus: 0x00000008 defers
// to Xid 154, 0x00000020 names RESET_GPU outright, and 0x00000002 says
// IGNORE. Having all three from one subcomponent makes each path testable
// without also exercising subcode matching.
const (
	deferToXid154 = "0x00000008"
	resetOutright = "0x00000020"
	ignoreRow     = "0x00000002"
)

// Driver versions either side of R565, where NVIDIA's Xid 154 linkage
// for this family begins.
var (
	driver575 = mustDriverVersion("575.51.03")
	driver560 = mustDriverVersion("560.35.03")
)

func mustDriverVersion(raw string) health.DriverVersion {
	v, ok := health.ParseDriverVersion(raw)
	if !ok {
		panic("mustDriverVersion: unparseable " + raw)
	}
	return v
}

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

// k145 and k146 name the per-code signal keys under test. Keying by code
// is what lets each gpu_xid_NNN check ask what its own code resolved to.
func k145(bucket string) string { return xid.NVLink5ResolutionKey(145, bucket) }
func k146(bucket string) string { return xid.NVLink5ResolutionKey(146, bucket) }

func TestNVLink5ResolutionCounts_TableRowNeedsNo154(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright),
	}, driver575)

	if got[k145(xid.ResolutionResetGPU)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k145(xid.ResolutionResetGPU))
	}
}

// TestNVLink5ResolutionCounts_KeyedPerCode pins the property the per-code
// checks depend on: two codes resolving to the same bucket must land in
// separate entries, or gpu_xid_145 fires on gpu_xid_146's fault.
func TestNVLink5ResolutionCounts_KeyedPerCode(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright), // 145 -> RESET_GPU
		unmatchedEvent("0019:01:00", true),         // 146 -> RESET_GPU_UNCONFIRMED
	}, driver575)

	if got[k145(xid.ResolutionResetGPU)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k145(xid.ResolutionResetGPU))
	}
	if got[k146(xid.ResolutionResetGPUUnconfirmed)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k146(xid.ResolutionResetGPUUnconfirmed))
	}
	if _, ok := got[xid.ResolutionResetGPU]; ok {
		t.Errorf("counts = %v, a bare bucket key is ambiguous across codes", got)
	}
}

func TestNVLink5ResolutionCounts_DeferralUsesThe154(t *testing.T) {
	tests := []struct {
		name    string
		toCode  string
		toName  string
		wantKey string
	}{
		{"reset required", "0x1", "GPU Reset Required", k145(xid.ResolutionResetGPU)},
		{"drain and reset", "0x4", "Drain and Reset", k145(xid.ResolutionResetGPU)},
		{"none clears it", "0x0", "None", k145(xid.ResolutionIgnore)},
		{"os reboot", "0x2", "OS Reboot", k145(xid.ResolutionRestartOS)},
		{"system reboot", "0x7", "System Reboot", k145(xid.ResolutionRestartOS)},
		{"drain p2p", "0x3", "Drain P2P", k145(xid.ResolutionDrainP2P)},
		{"bus reset", "0x6", "Bus Reset", k145(xid.ResolutionRestartOS)},
		{"imex domain stays unmapped", "0x5", "Recover IMEX Domain", k145(xid.ResolutionXid154Unrecognized)},
		{"unmapped code is surfaced", "0x9", "Some Future Action", k145(xid.ResolutionXid154Unrecognized)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
				srcTrackEvent("0019:01:00", deferToXid154),
				recoveryEvent("0019:01:00", "0x0", "None", tc.toCode, tc.toName),
			}, driver575)

			if got[tc.wantKey] != 1 {
				t.Errorf("counts = %v, want %s -> 1", got, tc.wantKey)
			}
			if _, ok := got[k145(xid.ResolutionXid154Eval)]; ok {
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
	}, driver560)

	if got[k145(xid.ResolutionXid154Missing)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k145(xid.ResolutionXid154Missing))
	}
	if got[k145(xid.ResolutionIgnore)] != 0 {
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
	}, driver575)

	if got[k145(xid.ResolutionResetGPU)] != 1 {
		t.Errorf("counts = %v, want exactly one %s (0019 only)", got, k145(xid.ResolutionResetGPU))
	}
	if got[k145(xid.ResolutionXid154Missing)] != 1 {
		t.Errorf("counts = %v, want exactly one %s (0018 only)", got, k145(xid.ResolutionXid154Missing))
	}
}

// TestNVLink5ResolutionCounts_CountsEventsNotGPUs pins the deliberate
// difference from recovery_action_counts, which counts GPUs: two faults
// on one GPU are two occurrences to investigate.
func TestNVLink5ResolutionCounts_CountsEventsNotGPUs(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright),
		srcTrackEvent("0019:01:00", resetOutright),
	}, driver575)

	if got[k145(xid.ResolutionResetGPU)] != 2 {
		t.Errorf("counts = %v, want %s -> 2", got, k145(xid.ResolutionResetGPU))
	}
}

func TestNVLink5ResolutionCounts_IgnoresNonNVLink5Events(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		{PCIAddress: "0019:01:00", XidCode: 79, ProcessName: "python3"},
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
	}, driver575)

	if len(got) != 0 {
		t.Errorf("counts = %v, want empty: no NVLink5-family events present", got)
	}
}

// TestNVLink5ResolutionCounts_UnmatchedFatalIsUnconfirmed separates a
// reset the table names from one the Fatal flag merely implies. Both
// escalate, but only one is documented guidance, and a verdict that
// cannot tell them apart overstates what NVIDIA actually said.
func TestNVLink5ResolutionCounts_UnmatchedFatalIsUnconfirmed(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		unmatchedEvent("0019:01:00", true),
	}, driver575)

	if got[k146(xid.ResolutionResetGPUUnconfirmed)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k146(xid.ResolutionResetGPUUnconfirmed))
	}
	if got[k146(xid.ResolutionResetGPU)] != 0 {
		t.Errorf("counts = %v, a fallback reset must not report as table-confirmed", got)
	}
}

func TestNVLink5ResolutionCounts_UnmatchedNonfatalStaysIgnore(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		unmatchedEvent("0019:01:00", false),
	}, driver575)

	if got[k146(xid.ResolutionIgnore)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k146(xid.ResolutionIgnore))
	}
}

// TestNVLink5ResolutionCounts_MatchedResetStaysConfirmed is the other
// half of the distinction: a row the table does cover must not be
// downgraded to unconfirmed.
func TestNVLink5ResolutionCounts_MatchedResetStaysConfirmed(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", resetOutright),
	}, driver575)

	if got[k145(xid.ResolutionResetGPU)] != 1 {
		t.Errorf("counts = %v, want %s -> 1", got, k145(xid.ResolutionResetGPU))
	}
	if _, ok := got[k145(xid.ResolutionResetGPUUnconfirmed)]; ok {
		t.Errorf("counts = %v, a table-confirmed row must not report as unconfirmed", got)
	}
}

// TestNVLink5ResolutionCounts_HexRenderingIsNormalised covers the log
// format permitting any hex rendering of a recovery-action code. Matching
// the printed text read "0x01" and "0X1" as codes this catalog does not
// know, turning a driver-requested reset into an unresolved WARNING.
func TestNVLink5ResolutionCounts_HexRenderingIsNormalised(t *testing.T) {
	for _, toCode := range []string{"0x1", "0x01", "0X1", "0x00000001"} {
		t.Run(toCode, func(t *testing.T) {
			got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
				srcTrackEvent("0019:01:00", deferToXid154),
				recoveryEvent("0019:01:00", "0x0", "None", toCode, "GPU Reset Required"),
			}, driver575)

			if got[k145(xid.ResolutionResetGPU)] != 1 {
				t.Errorf("counts = %v, want %s -> 1", got, k145(xid.ResolutionResetGPU))
			}
			if _, ok := got[k145(xid.ResolutionXid154Unrecognized)]; ok {
				t.Errorf("counts = %v, %q is a known action however it is spelled",
					got, toCode)
			}
		})
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

func codeVerdict(t *testing.T, checkID string, in resolutionInput) health.CheckVerdict {
	t.Helper()
	nv, err := hostCatalog(t, in.driverVersion).EvaluateAll(in.m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, checkID)
	if !ok {
		t.Fatalf("%s verdict not found", checkID)
	}
	return v
}

func TestNVLink5Check_ResolvedResetIsCritical(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", resetOutright)))

	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

// TestNVLink5Check_IgnoreDoesNotEscalate is why the resolution lives
// inside the per-code check rather than beside it. Roughly half the
// decode table's rows resolve to IGNORE, and a node's severity is the
// worst across all its checks — so an IGNORE-class fault only stops
// escalating if the check owning the code declines to escalate it.
func TestNVLink5Check_IgnoreDoesNotEscalate(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", ignoreRow)))

	if v.Severity != health.Info {
		t.Errorf("Severity = %v, want INFO (the table resolves this row to IGNORE)", v.Severity)
	}
	if v.Skipped {
		t.Errorf("verdict was skipped (%v), want an evaluated verdict", v.SkipReason)
	}
	if !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
	}
}

// TestNVLink5Check_IgnoreDoesNotEscalateAtNodeLevel is the same claim end
// to end: an ignorable fault leaves the node at INFO, so nothing else in
// the catalog escalates the event behind the check's back.
func TestNVLink5Check_IgnoreDoesNotEscalateAtNodeLevel(t *testing.T) {
	in := resolutionMeasurement("575.51.03", srcTrackEvent("0019:01:00", ignoreRow))
	nv, err := hostCatalog(t, in.driverVersion).EvaluateAll(in.m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Info {
		t.Errorf("OverallSeverity = %v, want INFO for an IGNORE-class fault", nv.OverallSeverity)
	}
}

func TestNVLink5Check_UnconfirmedResetIsCritical(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_146", resolutionMeasurement("575.51.03",
		unmatchedEvent("0019:01:00", true)))

	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

func TestNVLink5Check_MissingXid154IsWarning(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("560.35.03",
		srcTrackEvent("0019:01:00", deferToXid154)))

	if v.Severity != health.Warning {
		t.Errorf("Severity = %v, want WARNING", v.Severity)
	}
}

func TestNVLink5Check_UnrecognizedActionIsWarning(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", deferToXid154),
		recoveryEvent("0019:01:00", "0x0", "None", "0x9", "Some Future Action")))

	if v.Severity != health.Warning {
		t.Errorf("Severity = %v, want WARNING", v.Severity)
	}
}

// TestNVLink5Check_OnlyTheOwningCodeFires guards the per-code keying at
// the check level: a 145 fault must not light up gpu_xid_146.
func TestNVLink5Check_OnlyTheOwningCodeFires(t *testing.T) {
	m := resolutionMeasurement("575.51.03", srcTrackEvent("0019:01:00", resetOutright))

	if v := codeVerdict(t, "gpu_xid_145", m); v.Severity != health.Critical {
		t.Errorf("gpu_xid_145 Severity = %v, want CRITICAL", v.Severity)
	}
	if v := codeVerdict(t, "gpu_xid_146", m); v.Severity != health.Pass {
		t.Errorf("gpu_xid_146 Severity = %v, want PASS (no 146 event occurred)", v.Severity)
	}
}

func TestNVLink5Check_NoEventsIsPass(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("575.51.03"))

	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS", v.Severity)
	}
	// An empty resolution map still satisfies RequiredSignals; a skip
	// here would report the same PASS without having evaluated anything.
	if v.Skipped {
		t.Errorf("verdict was skipped (%v), want an evaluated PASS", v.SkipReason)
	}
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

// TestNVLink5Check_No154MeansRestartApp pins what a deferral with no Xid
// 154 recommends. The driver logs a 154 only when its recovery action
// changes, so its absence reads as no action requested: the same restart
// as an application-level Xid, not a service call.
func TestNVLink5Check_No154MeansRestartApp(t *testing.T) {
	nv, err := hostCatalog(t, "575.51.03").EvaluateAll(xid.XidMeasurement{
		Since:      "boot",
		XidCounts:  map[int64]int64{145: 1},
		Events:     []xid.XidEvent{srcTrackEvent("0019:01:00", deferToXid154)},
		EventCount: 1,
	})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_145")
	if !ok {
		t.Fatalf("gpu_xid_145 verdict not found")
	}
	if v.Severity != health.Warning {
		t.Errorf("severity = %v, want WARNING", v.Severity)
	}
	if want := health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

// TestNVLink5Check_UndecodedLineStillReports covers an NVLink5 event the
// sensor could not decode, which carries a code but no detail. It must
// still report rather than pass on an empty resolution signal.
func TestNVLink5Check_UndecodedLineStillReports(t *testing.T) {
	nv, err := hostCatalog(t, "575.51.03").EvaluateAll(xid.XidMeasurement{
		Since:      "boot",
		XidCounts:  map[int64]int64{150: 1},
		Events:     []xid.XidEvent{{PCIAddress: "0019:01:00", XidCode: 150}},
		EventCount: 1,
	})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_150")
	if !ok {
		t.Fatalf("gpu_xid_150 verdict not found")
	}
	if v.Severity != health.Warning {
		t.Errorf("severity = %v, want WARNING", v.Severity)
	}
}

// TestXid154Check_ReportsRequestedAction pins what gpu_xid_154 reports for
// each recovery action a GPU was left in. A 154 can accompany any Xid, so
// this is the only check that sees a request made alongside, say, an
// Xid 94.
func TestXid154Check_ReportsRequestedAction(t *testing.T) {
	restartOS := health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)
	reset := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired)
	service := health.Drain(health.RemediationActionServiceRequired)

	tests := []struct {
		name         string
		toCodes      []string
		wantAction   health.RecommendedAction
		wantSeverity health.Severity
	}{
		{name: "gpu reset", toCodes: []string{"0x1"}, wantSeverity: health.Critical, wantAction: reset},
		{name: "drain and reset", toCodes: []string{"0x4"}, wantSeverity: health.Critical, wantAction: reset},
		{name: "os reboot", toCodes: []string{"0x2"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "system reboot", toCodes: []string{"0x7"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "bus reset", toCodes: []string{"0x6"}, wantSeverity: health.Critical, wantAction: restartOS},
		{name: "drain p2p", toCodes: []string{"0x3"}, wantSeverity: health.Critical, wantAction: reset},
		{name: "unmapped", toCodes: []string{"0x5"}, wantSeverity: health.Warning, wantAction: service},
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
		})
	}
}

// TestNVLink5Check_DeferralToReboot covers a deferral the 154 resolves to
// a reboot, an action the decode table never names itself.
func TestNVLink5Check_DeferralToReboot(t *testing.T) {
	nv, err := hostCatalog(t, "575.51.03").EvaluateAll(xid.XidMeasurement{
		Since:     "boot",
		XidCounts: map[int64]int64{145: 1, 154: 1},
		Events: []xid.XidEvent{
			srcTrackEvent("0019:01:00", deferToXid154),
			recoveryEvent("0019:01:00", "0x0", "None", "0x2", "OS Reboot"),
		},
		EventCount: 2,
	})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_145")
	if !ok {
		t.Fatalf("gpu_xid_145 verdict not found")
	}
	want := health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)
	if v.Severity != health.Critical || !v.RecommendedAction.Equal(want) {
		t.Errorf("verdict = %v %v, want CRITICAL %v", v.Severity, v.RecommendedAction, want)
	}
}

// TestNVLink5Check_DeferralToDrainP2P covers a deferral the 154 resolves
// to Drain P2P, which takes the reset's steps and says what to do first.
func TestNVLink5Check_DeferralToDrainP2P(t *testing.T) {
	v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", deferToXid154),
		recoveryEvent("0019:01:00", "0x0", "None", "0x3", "Drain P2P")))

	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); v.Severity != health.Critical || !v.RecommendedAction.Equal(want) {
		t.Errorf("verdict = %v %v, want CRITICAL %v", v.Severity, v.RecommendedAction, want)
	}
	if !strings.Contains(strings.Join(v.Messages, " "), "persistence mode") {
		t.Errorf("Messages = %q, want the drain guidance", v.Messages)
	}
}

// TestNVLink5ResolutionCounts_Unread154IsUnrecognized covers a deferral
// whose GPU logged an Xid 154 the sensor could not read. There is a 154
// to defer to, so the fault is unresolved rather than missing its 154.
func TestNVLink5ResolutionCounts_Unread154IsUnrecognized(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0019:01:00", deferToXid154),
		{PCIAddress: "0019:01:00", XidCode: 154},
	}, driver575)
	want := map[string]int64{k145(xid.ResolutionXid154Unrecognized): 1}
	if !maps.Equal(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
}

// TestNVLink5Check_WarningTiesKeepTheirAction pins the WARNING rule
// order: when several WARNING buckets match on one code, the verdict
// carries the most cautious action rather than none.
func TestNVLink5Check_WarningTiesKeepTheirAction(t *testing.T) {
	undecoded := xid.XidEvent{PCIAddress: "0018:01:00", XidCode: 145}
	tests := []struct {
		name   string
		events []xid.XidEvent
		want   health.RecommendedAction
	}{
		{
			name:   "undecoded and missing",
			want:   health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired),
			events: []xid.XidEvent{undecoded, srcTrackEvent("0019:01:00", deferToXid154)},
		},
		{
			name: "missing and unrecognized",
			want: health.Drain(health.RemediationActionServiceRequired),
			events: []xid.XidEvent{
				srcTrackEvent("0019:01:00", deferToXid154),
				srcTrackEvent("001a:01:00", deferToXid154),
				recoveryEvent("001a:01:00", "0x0", "None", "0x5", "Recover IMEX Domain"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := codeVerdict(t, "gpu_xid_145", resolutionMeasurement("575.51.03", tt.events...))
			if v.Severity != health.Warning || !v.RecommendedAction.Equal(tt.want) {
				t.Errorf("verdict = %v %v, want WARNING %v", v.Severity, v.RecommendedAction, tt.want)
			}
		})
	}
}
