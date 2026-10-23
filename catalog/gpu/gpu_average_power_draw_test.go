// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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

func averagePowerMeasurement(powerW float64) gpu.GPUMeasurement {
	return gpu.GPUMeasurement{
		GPUID:             "GPU-0",
		AveragePowerDrawW: powerW,
	}
}

func averagePowerOverrides(minW, maxW float64) catalog.Option {
	return catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{
			CheckID:       "gpu_average_power_draw",
			ThresholdName: "min_power_w",
			Value:         minW,
		},
		catalog.Override{
			CheckID:       "gpu_average_power_draw",
			ThresholdName: "max_power_w",
			Value:         maxW,
		},
	))
}

func TestGPUAveragePowerDraw_Definition(t *testing.T) {
	definition := gpu.GPUAveragePowerDraw()
	if got, want := definition.Architectures, []string{"GB200", "GB300", "B200"}; !slices.Equal(got, want) {
		t.Errorf("Architectures = %v, want %v", got, want)
	}
	if definition.SchedulingCadence != health.Continuous {
		t.Errorf("SchedulingCadence = %v, want CONTINUOUS", definition.SchedulingCadence)
	}
	for name, want := range map[string]float64{"min_power_w": 65, "max_power_w": 1500} {
		spec := definition.Thresholds[name]
		if spec.Value != want {
			t.Errorf("%s default = %v, want %g", name, spec.Value, want)
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
			for _, powerW := range []float64{65, 220, 1500} {
				t.Run(strconv.FormatFloat(powerW, 'f', -1, 64), func(t *testing.T) {
					v, err := cat.Evaluate("gpu_average_power_draw", averagePowerMeasurement(powerW))
					if err != nil {
						t.Fatalf("Evaluate: %v", err)
					}
					if v.Severity != health.Pass {
						t.Errorf("Severity = %v, want PASS at %g W", v.Severity, powerW)
					}
					if got := v.Thresholds["min_power_w"].Value; got != float64(65) {
						t.Errorf("min_power_w = %v, want %s default 65", got, architecture)
					}
					if got := v.Thresholds["max_power_w"].Value; got != float64(1500) {
						t.Errorf("max_power_w = %v, want %s default 1500", got, architecture)
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
		powerW      float64
	}{
		{name: "below minimum", powerW: 64.999, messagePart: "below minimum 65 W"},
		{name: "above maximum", powerW: 1500.001, messagePart: "above maximum 1500 W"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := newCatalog().Evaluate("gpu_average_power_draw", averagePowerMeasurement(tt.powerW))
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if v.Severity != health.Critical {
				t.Fatalf("Severity = %v, want CRITICAL", v.Severity)
			}
			if !v.RecommendedAction.Equal(health.GenericDrain()) {
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
	cat := newCatalog(averagePowerOverrides(1800, 2500))
	v, err := cat.Evaluate("gpu_average_power_draw", averagePowerMeasurement(2000))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS with 1800-2500 W override", v.Severity)
	}
	for _, name := range []string{"min_power_w", "max_power_w"} {
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
	v, err := cat.Evaluate("gpu_average_power_draw", averagePowerMeasurement(220))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("skipped = %v, reason = %v, want ArchitectureUnsupported", v.Skipped, v.SkipReason)
	}
}

func TestGPUAveragePowerDraw_ErroredSignal(t *testing.T) {
	m := averagePowerMeasurement(0)
	m.AddError(errors.New("nvml average power query failed"), gpu.SignalAveragePowerDrawW)
	v, err := newCatalog().Evaluate("gpu_average_power_draw", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	if !v.RecommendedAction.Equal(health.Drain(health.RemediationActionCheckCollection)) {
		t.Errorf("RecommendedAction = %v, want drain/check_collection", v.RecommendedAction)
	}
}
