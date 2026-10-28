// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUSensorThrottle returns the check definition for GPU hardware
// throttling monitoring.
//
// This check monitors GPU throttling conditions by querying hardware
// slowdown events through NVML. It verifies that GPUs are not being
// throttled due to thermal, power, or other hardware conditions that
// could impact performance.
//
// The check fails CRITICAL when any of these hardware throttle
// reasons is active:
//   - hw_thermal_slowdown: GPU has crossed its thermal threshold
//     and is reducing clocks to avoid damage.
//   - hw_power_brake_slowdown: external power protection (PSU
//     undervolt, board-level) is clamping the GPU.
//   - hw_slowdown: generic hardware slowdown asserted by the
//     driver (typically a power/thermal interlock not
//     attributable to a specific cause).
//
// Otherwise PASS.
func GPUSensorThrottle() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_sensor_throttle",
		Name:        "GPU Sensor Throttle",
		Description: "Monitors GPU hardware throttling via NVML clocks-event-reasons. Active hardware slowdown, thermal slowdown, or power-brake slowdown indicates the GPU is being clamped by hardware protection logic.",
		Category:    health.GPU,

		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalHWSlowdown,
			SignalHWThermalSlowdown,
			SignalHWPowerBrakeSlowdown,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "B200", "H100", "H200", "A100"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Rules: []health.Rule{
			{
				Expression: "hw_thermal_slowdown == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: hardware thermal slowdown is active. GPU is throttling to avoid thermal damage.",
			},
			{
				Expression: "hw_power_brake_slowdown == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: hardware power-brake slowdown is active. External power protection is clamping the GPU.",
			},
			{
				Expression: "hw_slowdown == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: hardware slowdown is active. GPU is being clamped by hardware protection logic.",
			},
		},
	}
}
