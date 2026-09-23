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

// expected_gpu_pstate is a DefaultThreshold so fleets that idle at a
// different state can override it. The rule is an equality, so the
// override moves the passing state rather than widening a budget.
func overriddenPstateCatalog(pstate int64) *catalog.Catalog {
	return newCatalog(catalog.WithOverrides(catalog.NewOverrideSet(catalog.Override{
		CheckID:       "gpu_power_state",
		ThresholdName: "expected_gpu_pstate",
		Value:         pstate,
	})))
}

func wrongArchitectureCatalog() *catalog.Catalog {
	return catalog.New(
		catalog.WithArchitecture("V100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
}

func TestGPUPowerState(t *testing.T) {
	tests := []struct {
		cat              func() *catalog.Catalog
		name             string
		wantMessageParts []string
		wantAction       health.RecommendedAction
		measurement      gpu.GPUMeasurement
		wantSeverity     health.Severity
		wantSkipReason   health.SkipReason
		wantSkipped      bool
	}{
		{
			name:         "at expected pstate",
			cat:          func() *catalog.Catalog { return newCatalog() },
			measurement:  gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0},
			wantSeverity: health.Pass,
		},
		{
			name:             "below expected pstate",
			cat:              func() *catalog.Catalog { return newCatalog() },
			measurement:      gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 8},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0", "P8", "expected P0"},
		},
		{
			name:         "matches overridden expected pstate",
			cat:          func() *catalog.Catalog { return overriddenPstateCatalog(8) },
			measurement:  gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 8},
			wantSeverity: health.Pass,
		},
		{
			name:         "no longer matches default pstate once overridden",
			cat:          func() *catalog.Catalog { return overriddenPstateCatalog(8) },
			measurement:  gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0},
			wantSeverity: health.Critical,
		},
		{
			name:           "skipped on wrong architecture",
			cat:            wrongArchitectureCatalog,
			measurement:    gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0},
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			// A GPU whose performance state could not be collected propagates
			// to Critical regardless of the rule, since the data the check
			// needs is unavailable.
			name:         "errored GPU forces critical",
			cat:          func() *catalog.Catalog { return newCatalog() },
			measurement:  gpu.ErroredGPU("GPU-0", errors.New("nvml pstate query failed"), gpu.SignalGPUPstate),
			wantSeverity: health.Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("gpu_power_state", tt.measurement)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if v.Skipped != tt.wantSkipped || v.SkipReason != tt.wantSkipReason {
				t.Fatalf("skipped=%v reason=%v, want skipped=%v reason=%v", v.Skipped, v.SkipReason, tt.wantSkipped, tt.wantSkipReason)
			}
			if tt.wantSkipped {
				return
			}
			if v.Severity != tt.wantSeverity {
				t.Fatalf("severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if !tt.wantAction.IsZero() && !v.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tt.wantAction)
			}
			msg := strings.Join(v.Messages, " ")
			for _, want := range tt.wantMessageParts {
				if !strings.Contains(msg, want) {
					t.Errorf("Messages = %q, want %q interpolated", v.Messages, want)
				}
			}
		})
	}
}
