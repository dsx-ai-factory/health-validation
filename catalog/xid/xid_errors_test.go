package xid_test

import (
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
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionContactSupport); !v.RecommendedAction.Equal(want) {
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
	if want := health.Drain(health.RemediationActionResetGPU, health.RemediationActionContactSupport); !critical[0].RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", critical[0].RecommendedAction, want)
	}
}
