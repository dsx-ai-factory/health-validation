// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUAveragePowerDraw returns the check definition that verifies each GPU's
// one-second average power draw is within its expected range.
func GPUAveragePowerDraw() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_average_power_draw",
		Name:        "GPU Average Power Draw",
		Description: "Verifies each GPU's one-second average power draw is within the expected range.",
		Category:    health.GPU,

		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalAveragePowerDrawW,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"min_power_w": {
				Value:       float64(65),
				ValueType:   health.ThresholdFloat64,
				ValueMin:    float64(0),
				Category:    health.DefaultThreshold,
				Description: "Minimum expected one-second average power draw per GPU. Override per fleet.",
				Unit:        "W",
			},
			"max_power_w": {
				Value:       float64(1500),
				ValueType:   health.ThresholdFloat64,
				ValueMin:    float64(1),
				Category:    health.DefaultThreshold,
				Description: "Maximum expected one-second average power draw per GPU. Override per fleet.",
				Unit:        "W",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "average_power_draw_w < min_power_w",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: average power draw ${average_power_draw_w} W is below minimum ${min_power_w} W.",
			},
			{
				Expression: "average_power_draw_w > max_power_w",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: average power draw ${average_power_draw_w} W is above maximum ${max_power_w} W.",
			},
		},
	}
}
