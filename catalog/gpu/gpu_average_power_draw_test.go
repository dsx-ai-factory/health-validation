package gpu_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func averagePowerMeasurement(powerMW int64) gpu.GPUMeasurement {
	return gpu.GPUMeasurement{
		GPUID:              "GPU-0",
		AveragePowerDrawMW: powerMW,
	}
}

func averagePowerOverrides(minMW, maxMW int64) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{
			CheckID:       "gpu_average_power_draw",
			ThresholdName: "min_power_mw",
			Value:         minMW,
		},
		catalog.Override{
			CheckID:       "gpu_average_power_draw",
			ThresholdName: "max_power_mw",
			Value:         maxMW,
		},
	))
}

func TestGPUAveragePowerDraw_Definition(t *testing.T) {
	definition := gpu.GPUAveragePowerDraw()
	if got, want := definition.Architectures, []string{"GB200", "GB300"}; !slices.Equal(got, want) {
		t.Errorf("Architectures = %v, want %v", got, want)
	}
	if definition.SchedulingCadence != health.Continuous {
		t.Errorf("SchedulingCadence = %v, want CONTINUOUS", definition.SchedulingCadence)
	}
	for name, want := range map[string]int64{"min_power_mw": 65_000, "max_power_mw": 1_500_000} {
		spec := definition.Thresholds[name]
		if spec.Value != want {
			t.Errorf("%s default = %v, want %d", name, spec.Value, want)
		}
		if len(spec.PerArchitecture) != 0 {
			t.Errorf("%s PerArchitecture = %v, want empty for shared GB200/GB300 defaults", name, spec.PerArchitecture)
		}
	}
}

func TestGPUAveragePowerDraw_UsesSupportedArchitectureDefaults(t *testing.T) {
	for _, architecture := range []string{"GB200", "GB300"} {
		t.Run(architecture, func(t *testing.T) {
			cat := catalog.New(
				catalog.WithArchitecture(architecture),
				catalog.WithExecutionContext(health.OperatorInfra),
			)
			for _, powerMW := range []int64{65_000, 220_000, 1_500_000} {
				t.Run(strconv.FormatInt(powerMW, 10), func(t *testing.T) {
					v, err := cat.Evaluate("gpu_average_power_draw", averagePowerMeasurement(powerMW))
					if err != nil {
						t.Fatalf("Evaluate: %v", err)
					}
					if v.Severity != health.Pass {
						t.Errorf("Severity = %v, want PASS at %d mW", v.Severity, powerMW)
					}
					if got := v.Thresholds["min_power_mw"].Value; got != int64(65_000) {
						t.Errorf("min_power_mw = %v, want %s default 65000", got, architecture)
					}
					if got := v.Thresholds["max_power_mw"].Value; got != int64(1_500_000) {
						t.Errorf("max_power_mw = %v, want %s default 1500000", got, architecture)
					}
				})
			}
		})
	}
}

func TestGPUAveragePowerDraw_OutOfRange(t *testing.T) {
	tests := []struct {
		name        string
		messagePart string
		powerMW     int64
	}{
		{name: "below minimum", powerMW: 64_999, messagePart: "below minimum 65000 mW"},
		{name: "above maximum", powerMW: 1_500_001, messagePart: "above maximum 1500000 mW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := newCatalog().Evaluate("gpu_average_power_draw", averagePowerMeasurement(tt.powerMW))
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
			for _, part := range []string{"GPU-0", tt.messagePart} {
				if !strings.Contains(message, part) {
					t.Errorf("Messages = %q, want %q", v.Messages, part)
				}
			}
		})
	}
}

func TestGPUAveragePowerDraw_OverridesBounds(t *testing.T) {
	// 2000 W is outside the default (65-1500 W) but within this override.
	cat := newCatalog(averagePowerOverrides(1_800_000, 2_500_000))
	v, err := cat.Evaluate("gpu_average_power_draw", averagePowerMeasurement(2_000_000))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS with 1800-2500 W override", v.Severity)
	}
	for _, name := range []string{"min_power_mw", "max_power_mw"} {
		if !v.Thresholds[name].Overridden {
			t.Errorf("%s = %+v, want operator override", name, v.Thresholds[name])
		}
	}
}

func TestGPUAveragePowerDraw_SkipsOnWrongArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("H100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	v, err := cat.Evaluate("gpu_average_power_draw", averagePowerMeasurement(220_000))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("skipped = %v, reason = %v, want ArchitectureUnsupported", v.Skipped, v.SkipReason)
	}
}

func TestGPUAveragePowerDraw_ErroredSignal(t *testing.T) {
	m := averagePowerMeasurement(0)
	m.AddError(errors.New("nvml average power query failed"), gpu.SignalAveragePowerDrawMW)
	v, err := newCatalog().Evaluate("gpu_average_power_draw", m)
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
