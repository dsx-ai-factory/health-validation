package gpu_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// powerLimitOverrides configures expected_power_limit_w for gpu_power_limit.
func powerLimitOverrides(limitW float64) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "gpu_power_limit",
		ThresholdName: "expected_power_limit_w",
		Value:         limitW,
	}))
}

// matchingPowerLimits builds a GPUMeasurement whose current and enforced
// limits both match limitW.
func matchingPowerLimits(limitW float64) gpu.GPUMeasurement {
	return gpu.GPUMeasurement{
		GPUID:               "GPU-0",
		PowerLimitW:         limitW,
		EnforcedPowerLimitW: limitW,
	}
}

// requirePowerLimitCritical asserts CRITICAL with check_mechanicals and
// that each messageParts substring appears in the verdict messages.
func requirePowerLimitCritical(t *testing.T, cat *catalog.Catalog, m gpu.GPUMeasurement, messageParts ...string) {
	t.Helper()
	v, err := cat.Evaluate("gpu_power_limit", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Fatalf("Severity = %v, want CRITICAL", v.Severity)
	}
	if v.RecommendedAction != health.Drain(health.DiagnosticActionCheckMechanicals) {
		t.Errorf("RecommendedAction = %v, want drain/check_mechanicals", v.RecommendedAction)
	}
	message := strings.Join(v.Messages, " ")
	for _, part := range messageParts {
		if !strings.Contains(message, part) {
			t.Errorf("Messages = %q, want %q", v.Messages, part)
		}
	}
}

// Evaluates matching watt signals with no overrides and expects PASS
// against each architecture's catalog default.
func TestGPUPowerLimit_UsesArchitectureDefault(t *testing.T) {
	for _, tc := range []struct {
		arch   string
		limitW float64
	}{
		{arch: "GB200", limitW: 1200},
		{arch: "GB300", limitW: 1400},
		{arch: "H100", limitW: 700},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			cat := catalog.New(
				catalog.WithArchitecture(tc.arch),
				catalog.WithExecutionContext(health.OperatorInfra),
			)
			v, err := cat.Evaluate("gpu_power_limit", matchingPowerLimits(tc.limitW))
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if v.Severity != health.Pass {
				t.Errorf("Severity = %v, want PASS", v.Severity)
			}
		})
	}
}

// Evaluates each mismatched signal and asserts CRITICAL + check_mechanicals.
func TestGPUPowerLimit_Mismatches(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		m := matchingPowerLimits(1200)
		m.PowerLimitW = 1100
		requirePowerLimitCritical(t, newCatalog(), m,
			"GPU-0", "current", "1100 W", "expected 1200 W")
	})

	t.Run("enforced", func(t *testing.T) {
		m := matchingPowerLimits(1200)
		m.EnforcedPowerLimitW = 1100
		requirePowerLimitCritical(t, newCatalog(), m,
			"GPU-0", "enforced", "1100 W", "expected 1200 W")
	})
}

// Evaluates 1199.6 W against an expected 1200 W and expects CRITICAL.
// The fractional-watt signal must not round into a pass.
func TestGPUPowerLimit_NonIntegralWattDoesNotRound(t *testing.T) {
	m := matchingPowerLimits(1200)
	m.PowerLimitW = 1199.6
	requirePowerLimitCritical(t, newCatalog(), m,
		"1199.6 W", "expected 1200 W")
}

// Evaluates an operator override of the architecture default and expects PASS.
func TestGPUPowerLimit_OverrideArchitectureDefault(t *testing.T) {
	// GB200 default is 1200 W; a 1150 W fleet still used by one template must override.
	cat := newCatalog(powerLimitOverrides(1150))
	v, err := cat.Evaluate("gpu_power_limit", matchingPowerLimits(1150))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS", v.Severity)
	}
}

// Evaluates on V100 and expects ArchitectureUnsupported.
func TestGPUPowerLimit_SkipsOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("V100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	v, err := cat.Evaluate("gpu_power_limit", matchingPowerLimits(1200))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("skipped = %v, reason = %v, want ArchitectureUnsupported", v.Skipped, v.SkipReason)
	}
}

// Evaluates a measurement with AddError on power_limit_w and expects
// CRITICAL + check_collection, not CEL against a zero value.
func TestGPUPowerLimit_ErroredSignal(t *testing.T) {
	m := matchingPowerLimits(1200)
	m.AddError(errors.New("nvml power limit query failed"), gpu.SignalPowerLimitW)
	v, err := newCatalog().Evaluate("gpu_power_limit", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	if v.RecommendedAction != health.Drain(health.DiagnosticActionCheckCollection) {
		t.Errorf("RecommendedAction = %v, want drain/check_collection", v.RecommendedAction)
	}
}
