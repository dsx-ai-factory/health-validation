// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"maps"
	"slices"
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

// TestNVLink5ResolutionCounts_DeferralKeepsItsBucket pins that a deferral
// with an Xid 154 for its GPU stays XID_154_EVAL whatever the 154 asked
// for: the action is gpu_xid_154's to report, not the NVLink5 check's.
// An unreadable 154 still counts as one logged.
func TestNVLink5ResolutionCounts_DeferralKeepsItsBucket(t *testing.T) {
	for _, recovery := range []xid.XidEvent{
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
		recoveryEvent("0019:01:00", "0x0", "None", "0x2", "OS Reboot"),
		recoveryEvent("0019:01:00", "0x0", "None", "0x0", "None"),
		{PCIAddress: "0019:01:00", XidCode: 154},
	} {
		got := xid.NVLink5ResolutionCounts([]xid.XidEvent{srcTrackEvent("0019:01:00", deferToXid154), recovery}, driver575)
		if want := map[string]int64{k145(xid.ResolutionXid154Eval): 1}; !maps.Equal(got, want) {
			t.Errorf("with %+v: counts = %v, want %v", recovery.RecoveryAction, got, want)
		}
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
// Xid 154s per PCI address. Both GPUs report the identical fault at the
// identical ErrorStatus in one window; only one logs an Xid 154. Joining
// by window rather than by GPU would defer both to an Xid 154 that only
// one GPU logged.
func TestNVLink5ResolutionCounts_ResolvedPerGPU(t *testing.T) {
	got := xid.NVLink5ResolutionCounts([]xid.XidEvent{
		srcTrackEvent("0018:01:00", deferToXid154),
		srcTrackEvent("0019:01:00", deferToXid154),
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"),
	}, driver575)

	if got[k145(xid.ResolutionXid154Eval)] != 1 {
		t.Errorf("counts = %v, want exactly one %s (0019 only)", got, k145(xid.ResolutionXid154Eval))
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

// TestNVLink5Check_DeferralPointsToXid154 pins the split between the two
// checks: a deferred Xid 145 reports INFO and leaves the driver's
// requested action to gpu_xid_154, so it is not reported twice.
func TestNVLink5Check_DeferralPointsToXid154(t *testing.T) {
	m := resolutionMeasurement("575.51.03",
		srcTrackEvent("0019:01:00", deferToXid154),
		recoveryEvent("0019:01:00", "0x0", "None", "0x1", "GPU Reset Required"))

	v := codeVerdict(t, "gpu_xid_145", m)
	if v.Severity != health.Info || !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("gpu_xid_145 = %v %v, want INFO with no action", v.Severity, v.RecommendedAction)
	}
	if !strings.Contains(strings.Join(v.Messages, " "), "gpu_xid_154") {
		t.Errorf("Messages = %q, want a pointer to gpu_xid_154", v.Messages)
	}
	if v := codeVerdict(t, "gpu_xid_154", m); v.Severity != health.Critical {
		t.Errorf("gpu_xid_154 Severity = %v, want CRITICAL", v.Severity)
	}
}

// TestNVLink5Check_OnlyDeferringCodesHaveDeferralRules pins that the
// Xid 154 deferral rules exist only on codes whose decode-table rows
// defer, which in NVIDIA's table is only Xid 145.
func TestNVLink5Check_OnlyDeferringCodesHaveDeferralRules(t *testing.T) {
	for _, def := range xid.Checks() {
		code, ok := strings.CutPrefix(def.ID, "gpu_xid_")
		if !ok || !slices.Contains([]string{"144", "145", "146", "147", "148", "149", "150"}, code) {
			continue
		}
		var deferral bool
		for _, r := range def.Rules {
			if strings.Contains(r.Expression, "XID_154") {
				deferral = true
			}
		}
		if deferral != (code == "145") {
			t.Errorf("%s has deferral rules = %v, want %v", def.ID, deferral, code == "145")
		}
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
	if want := health.Drain(health.RemediationActionRestartApp); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

// TestNVLink5Check_UndecodedLineStillReports covers an NVLink5 event the
// sensor could not decode, which carries a code but no detail. It must
// still report rather than pass on an empty resolution signal, and flag
// the node for watching without naming a repair it cannot justify.
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
	if !v.RecommendedAction.Equal(health.Monitor) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, health.Monitor)
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
			want:   health.Drain(health.RemediationActionRestartApp),
			events: []xid.XidEvent{undecoded, srcTrackEvent("0019:01:00", deferToXid154)},
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
