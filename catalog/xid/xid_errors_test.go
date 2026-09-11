package xid_test

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func xidCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	for _, cd := range xid.Checks() {
		cat.MustRegister(cd)
	}
	return cat
}

// measurementFor builds an XidMeasurement directly from raw Xid codes —
// the catalog-native way, with no dmesg parsing. Parsing "NVRM: Xid …"
// log lines is the toolkit's concern (toolkit/xid.ParseXid) and is tested
// there; the catalog only needs the aggregated XidCounts/EventCount.
func measurementFor(codes ...int64) xid.XidMeasurement {
	m := xid.XidMeasurement{Since: "boot"}
	for _, code := range codes {
		m.Events = append(m.Events, xid.XidEvent{PCIAddress: "0000:01:00", XidCode: code, Pid: 1, ProcessName: "app"})
		if m.XidCounts == nil {
			m.XidCounts = make(map[int64]int64)
		}
		m.XidCounts[code]++
		m.EventCount++
	}
	return m
}

func evalXid(t *testing.T, codes ...int64) health.NodeVerdict {
	t.Helper()
	nv, err := xidCatalog(t).EvaluateAll(measurementFor(codes...))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	return nv
}

func TestXidErrors_SeverityByBucket(t *testing.T) {
	tests := []struct {
		name string
		code int64
		want health.Severity
	}{
		{"reset_gpu (46)", 46, health.Critical},
		{"fell off bus (79)", 79, health.Critical},
		{"memory remapping failure (64)", 64, health.Critical},
		{"double bit ECC error (48)", 48, health.Critical},
		{"app fault (11)", 11, health.Warning},
		{"graphics engine exception (13)", 13, health.Warning},
		{"memory page fault (31)", 31, health.Warning},
		{"vgpu start (78)", 78, health.Warning},
		{"preemptive removal (45)", 45, health.Warning},
		{"recovery action changed (154)", 154, health.Info},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nv := evalXid(t, tc.code)
			if nv.OverallSeverity != tc.want {
				t.Errorf("code %d: OverallSeverity = %v, want %v", tc.code, nv.OverallSeverity, tc.want)
			}
		})
	}
}

func TestXidErrors_WarningActionByBucket(t *testing.T) {
	tests := []struct {
		name string
		want health.RecommendedAction
		code int64
	}{
		{"app fault (13)", health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired), 13},
		{"memory page fault (31)", health.Drain(health.RemediationActionRestartApp, health.RemediationActionServiceRequired), 31},
		{"preemptive removal (45)", health.Drain(health.RemediationActionWorkflowXid45), 45},
		{"vgpu start (78)", health.Drain(health.RemediationActionUpdateSWFW), 78},
		{"key rotation (151)", health.Drain(health.RemediationActionRestartOS), 151},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := checkResult(evalXid(t, tc.code), "gpu_xid_warning")
			if !ok {
				t.Fatalf("gpu_xid_warning verdict not found")
			}
			if !v.RecommendedAction.Equal(tc.want) {
				t.Errorf("code %d: RecommendedAction = %v, want %v", tc.code, v.RecommendedAction, tc.want)
			}
		})
	}
}

// nvlink5MeasurementFor builds an XidMeasurement for one NVLink5-family
// event (144-150), the way toolkit/xid.ParseXid would after decoding the
// extended Fatal/Nonfatal log format.
func nvlink5MeasurementFor(code int64, fatal bool) xid.XidMeasurement {
	m := xid.XidMeasurement{
		Since:     "boot",
		XidCounts: map[int64]int64{code: 1},
		Events: []xid.XidEvent{{
			PCIAddress: "0000:01:00",
			XidCode:    code,
			NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Fatal: fatal, Link: 8},
		}},
		EventCount: 1,
	}
	if fatal {
		m.FatalXidCounts = map[int64]int64{code: 1}
	}
	return m
}

func TestXidErrors_NVLink5_FatalIsCritical(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5MeasurementFor(146, true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
	v, ok := checkResult(nv, "gpu_xid_146")
	if !ok {
		t.Fatalf("gpu_xid_146 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_146 severity = %v, want CRITICAL", v.Severity)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

func TestXidErrors_NVLink5_NonfatalIsWarningNoAction(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5MeasurementFor(146, false))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Warning {
		t.Errorf("OverallSeverity = %v, want WARNING", nv.OverallSeverity)
	}
	v, ok := checkResult(nv, "gpu_xid_146")
	if !ok {
		t.Fatalf("gpu_xid_146 verdict not found")
	}
	if v.Severity != health.Warning {
		t.Errorf("gpu_xid_146 severity = %v, want WARNING", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
	}
}

// A Fatal occurrence also counts toward xid_counts; it must not
// additionally be reported as Nonfatal (issue #126).
func TestXidErrors_NVLink5_FatalIsNotAlsoNonfatal(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5MeasurementFor(149, true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149")
	if !ok {
		t.Fatalf("gpu_xid_149 verdict not found")
	}
	want := []string{"GPU Xid 149 detected: NVLink NETIR error (Fatal)."}
	if !slices.Equal(v.Messages, want) {
		t.Errorf("Messages = %q, want %q", v.Messages, want)
	}
}

func TestXidErrors_NVLink5_FatalAndNonfatalReportsBoth(t *testing.T) {
	m := nvlink5MeasurementFor(149, true)
	m.XidCounts[149] = 2
	m.EventCount = 2
	m.Events = append(m.Events, xid.XidEvent{
		PCIAddress: "0000:01:00",
		XidCode:    149,
		NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Link: 9},
	})
	nv, err := xidCatalog(t).EvaluateAll(m)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149")
	if !ok {
		t.Fatalf("gpu_xid_149 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_149 severity = %v, want CRITICAL", v.Severity)
	}
	want := []string{
		"GPU Xid 149 detected: NVLink NETIR error (Fatal).",
		"GPU Xid 149 detected: NVLink NETIR error (Nonfatal).",
	}
	if !slices.Equal(v.Messages, want) {
		t.Errorf("Messages = %q, want %q", v.Messages, want)
	}
}

// nvlink5SubXidCodeMeasurementFor builds an XidMeasurement for one Xid 149
// NETIR_LINK_EVT/NETIR_LINK_DOWN event carrying the given subcode, the way
// toolkit/xid.ParseXid would after decoding the extended log format.
func nvlink5SubXidCodeMeasurementFor(subcode string, fatal bool) xid.XidMeasurement {
	m := xid.XidMeasurement{
		Since:            "boot",
		XidCounts:        map[int64]int64{149: 1},
		SubXidCodeCounts: map[string]int64{subcode: 1},
		Events: []xid.XidEvent{{
			PCIAddress: "0000:01:00",
			XidCode:    149,
			NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", Fatal: fatal, Link: 8, SubXidCode: subcode},
		}},
		EventCount: 1,
	}
	if fatal {
		m.FatalXidCounts = map[int64]int64{149: 1}
	}
	return m
}

func TestXidErrors_NVLink5SubXidCode_Critical(t *testing.T) {
	nv, err := xidCatalog(t).EvaluateAll(nvlink5SubXidCodeMeasurementFor("149.4", true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149_4")
	if !ok {
		t.Fatalf("gpu_xid_149_4 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_149_4 severity = %v, want CRITICAL", v.Severity)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

func TestXidErrors_NVLink5SubXidCode_Warning(t *testing.T) {
	// Subcode 149.26 is the one row whose decode-table resolution is
	// IGNORE rather than RESET_GPU.
	nv, err := xidCatalog(t).EvaluateAll(nvlink5SubXidCodeMeasurementFor("149.26", false))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149_26")
	if !ok {
		t.Fatalf("gpu_xid_149_26 verdict not found")
	}
	if v.Severity != health.Warning {
		t.Errorf("gpu_xid_149_26 severity = %v, want WARNING", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
	}
}

func TestXidErrors_NVLink5SubXidCode_AdditiveWithBaseline(t *testing.T) {
	// A "149.4" Fatal event fires both the per-subcode check and the
	// baseline gpu_xid_149 Fatal/Nonfatal check: subcode checks are
	// additive, not a replacement.
	nv, err := xidCatalog(t).EvaluateAll(nvlink5SubXidCodeMeasurementFor("149.4", true))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if _, ok := checkResult(nv, "gpu_xid_149"); !ok {
		t.Errorf("gpu_xid_149 (baseline) verdict not found")
	}
	if _, ok := checkResult(nv, "gpu_xid_149_4"); !ok {
		t.Errorf("gpu_xid_149_4 (subcode) verdict not found")
	}
}

func checkResult(nv health.NodeVerdict, checkID string) (health.CheckVerdict, bool) {
	for _, v := range nv.CheckResults {
		if v.CheckID == checkID {
			return v, true
		}
	}
	return health.CheckVerdict{}, false
}

func TestXidErrors_IgnoredCode_Pass(t *testing.T) {
	// Xid 63 (memory remapping event) has immediate action IGNORE and is
	// not in any bucket, so it must not fire.
	nv := evalXid(t, 63)
	if nv.OverallSeverity != health.Pass {
		t.Errorf("ignored code 63: OverallSeverity = %v, want PASS", nv.OverallSeverity)
	}
}

func TestXidErrors_NoEvents_Pass(t *testing.T) {
	nv := evalXid(t)
	if nv.OverallSeverity != health.Pass {
		t.Errorf("clean boot: OverallSeverity = %v, want PASS", nv.OverallSeverity)
	}
}

func TestXidErrors_WorstSeverityWins(t *testing.T) {
	// An app fault (WARNING) alongside a reset-class fault (CRITICAL)
	// aggregates to CRITICAL.
	nv := evalXid(t, 11, 79)
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
}

func TestXidErrors_PerCodeCriticalCheck(t *testing.T) {
	// Xid 79 fires its own gpu_xid_79 check (CRITICAL, reset+escalate); every
	// other Xid check runs against the same measurement but passes.
	nv := evalXid(t, 79)

	var critical []health.CheckVerdict
	for _, v := range nv.CheckResults {
		if v.Severity == health.Critical {
			critical = append(critical, v)
		}
	}
	if len(critical) != 1 {
		t.Fatalf("got %d CRITICAL verdicts, want 1 (only gpu_xid_79)", len(critical))
	}
	if critical[0].CheckID != "gpu_xid_79" {
		t.Errorf("CheckID = %q, want gpu_xid_79", critical[0].CheckID)
	}
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired); !critical[0].RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", critical[0].RecommendedAction, want)
	}
}
