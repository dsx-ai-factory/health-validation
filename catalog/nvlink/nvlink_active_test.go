package nvlink_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/NVIDIA/health-validation/catalog"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
)

func TestNVLinkActive_AllActive(t *testing.T) {
	cat := fabricCatalog()

	v, err := cat.Evaluate("nvlink_active", nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (0 inactive == default budget)", v.Severity)
	}
}

func TestNVLinkActive_OneInactive(t *testing.T) {
	cat := fabricCatalog()

	v, err := cat.Evaluate("nvlink_active", nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (1 > default 0)", v.Severity)
	}
	if v.RecommendedAction != health.Drain(health.DiagnosticActionRetestHardware) {
		t.Errorf("RecommendedAction = %v, want retest_hardware", v.RecommendedAction)
	}
	msg := strings.Join(v.Messages, " ")
	for _, want := range []string{"GPU-0", "1 inactive", "allowed: 0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Messages = %q, want %q interpolated", v.Messages, want)
		}
	}
}

// max_inactive_nvlinks is a DefaultThreshold so operators can carve out a
// per-GPU tolerated budget.
func TestNVLinkActive_OverrideAllowsBudget(t *testing.T) {
	cat := fabricCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "nvlink_active",
		ThresholdName: "max_inactive_nvlinks",
		Value:         int64(2),
	})))

	v, err := cat.Evaluate("nvlink_active", nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (2 == overridden budget)", v.Severity)
	}

	v, err = cat.Evaluate("nvlink_active", nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (3 > overridden 2)", v.Severity)
	}
}

func TestNVLinkActive_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("V100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("nvlink_active", nvlink.NVLinkMeasurement{GPUID: "GPU-0", InactiveCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}

// A GPU whose link state could not be collected propagates to Critical
// regardless of the rule, since the data the check needs is unavailable.
func TestNVLinkActive_ErroredGPU(t *testing.T) {
	cat := fabricCatalog()

	m := nvlink.ErroredNVLink("GPU-0", errors.New("nvml link-state query failed"), nvlink.SignalInactiveCount)
	v, err := cat.Evaluate("nvlink_active", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (errored GPU)", v.Severity)
	}
}
