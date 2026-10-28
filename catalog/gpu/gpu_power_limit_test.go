// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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

func TestGPUPowerLimit_OmitsBaseValue(t *testing.T) {
	spec := gpu.GPUPowerLimit().Thresholds["expected_power_limit_w"]
	if spec.Value != nil {
		t.Errorf("Value = %v, want nil (PerArchitecture covers every declared architecture)", spec.Value)
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
		{arch: "VR200", limitW: 2300},
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

func TestGPUPowerLimit_Scenarios(t *testing.T) {
	tests := []struct {
		cat              func() *catalog.Catalog
		measurement      func() gpu.GPUMeasurement
		name             string
		wantMessageParts []string
		wantAction       health.RecommendedAction
		wantSeverity     health.Severity
		wantSkipReason   health.SkipReason
		wantSkipped      bool
	}{
		{
			name: "current limit mismatch",
			cat:  func() *catalog.Catalog { return newCatalog() },
			measurement: func() gpu.GPUMeasurement {
				m := matchingPowerLimits(1200)
				m.PowerLimitW = 1100
				return m
			},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0", "current", "1100 W", "expected 1200 W"},
		},
		{
			name: "enforced limit mismatch",
			cat:  func() *catalog.Catalog { return newCatalog() },
			measurement: func() gpu.GPUMeasurement {
				m := matchingPowerLimits(1200)
				m.EnforcedPowerLimitW = 1100
				return m
			},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0", "enforced", "1100 W", "expected 1200 W"},
		},
		{
			// The fractional-watt signal must not round into a pass.
			name: "non-integral watt does not round",
			cat:  func() *catalog.Catalog { return newCatalog() },
			measurement: func() gpu.GPUMeasurement {
				m := matchingPowerLimits(1200)
				m.PowerLimitW = 1199.6
				return m
			},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"1199.6 W", "expected 1200 W"},
		},
		{
			// GB200 default is 1200 W; a 1150 W fleet still used by one
			// template must be able to override it.
			name:         "override moves the architecture default",
			cat:          func() *catalog.Catalog { return newCatalog(powerLimitOverrides(1150)) },
			measurement:  func() gpu.GPUMeasurement { return matchingPowerLimits(1150) },
			wantSeverity: health.Pass,
		},
		{
			name: "skipped on wrong architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("V100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement:    func() gpu.GPUMeasurement { return matchingPowerLimits(1200) },
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			// A read failure must fail the check, not fall through to CEL
			// against a zero value.
			name: "errored signal forces critical",
			cat:  func() *catalog.Catalog { return newCatalog() },
			measurement: func() gpu.GPUMeasurement {
				m := matchingPowerLimits(1200)
				m.AddError(errors.New("nvml power limit query failed"), gpu.SignalPowerLimitW)
				return m
			},
			wantSeverity: health.Critical,
			wantAction:   health.Drain(health.RemediationActionCheckCollection),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("gpu_power_limit", tt.measurement())
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
				t.Fatalf("Severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if !tt.wantAction.IsZero() && !v.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tt.wantAction)
			}
			message := strings.Join(v.Messages, " ")
			for _, part := range tt.wantMessageParts {
				if !strings.Contains(message, part) {
					t.Errorf("Messages = %q, want %q", v.Messages, part)
				}
			}
		})
	}
}
