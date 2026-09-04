package gpu_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func newCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

func TestGPUPowerState_AtExpected(t *testing.T) {
	cat := newCatalog()

	v, err := cat.Evaluate("gpu_power_state", gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (P0 == default expected P0)", v.Severity)
	}
}

func TestGPUPowerState_BelowExpected(t *testing.T) {
	cat := newCatalog()

	v, err := cat.Evaluate("gpu_power_state", gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 8})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (P8 != default expected P0)", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.GenericDrain()) {
		t.Errorf("RecommendedAction = %v, want drain/reboot", v.RecommendedAction)
	}
	msg := strings.Join(v.Messages, " ")
	for _, want := range []string{"GPU-0", "P8", "expected P0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Messages = %q, want %q interpolated", v.Messages, want)
		}
	}
}

// expected_gpu_pstate is a DefaultThreshold so fleets that idle at a
// different state can override it. The rule is an equality, so the
// override moves the passing state rather than widening a budget.
func TestGPUPowerState_OverrideExpected(t *testing.T) {
	cat := newCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "gpu_power_state",
		ThresholdName: "expected_gpu_pstate",
		Value:         int64(8),
	})))

	v, err := cat.Evaluate("gpu_power_state", gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 8})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("severity = %v, want PASS (P8 == overridden expected)", v.Severity)
	}

	v, err = cat.Evaluate("gpu_power_state", gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (P0 != overridden expected P8)", v.Severity)
	}
}

func TestGPUPowerState_SkippedOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("V100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)

	v, err := cat.Evaluate("gpu_power_state", gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("expected skipped with ArchitectureUnsupported, got skipped=%v reason=%v", v.Skipped, v.SkipReason)
	}
}

// A GPU whose performance state could not be collected propagates to
// Critical regardless of the rule, since the data the check needs is
// unavailable.
func TestGPUPowerState_ErroredGPU(t *testing.T) {
	cat := newCatalog()

	m := gpu.ErroredGPU("GPU-0", errors.New("nvml pstate query failed"), gpu.SignalGPUPstate)
	v, err := cat.Evaluate("gpu_power_state", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("severity = %v, want CRITICAL (errored GPU)", v.Severity)
	}
}
