// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPowerState returns the check definition that verifies each GPU is
// operating at the expected performance state (pstate).
//
// The check fires Critical when a GPU's pstate differs from
// expected_gpu_pstate or, where idle_gpu_pstate is set, falls outside the
// range from expected_gpu_pstate to idle_gpu_pstate. Each is the numeric
// level N in P<N> (0 = P0, maximum performance); idle_gpu_pstate is -1
// (none) except on VR, which moves between P0 and P5 with load.
func GPUPowerState() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_power_state",
		Name:        "GPU Power State",
		Description: "Verifies each GPU is operating at the expected performance state.",
		Category:    health.GPU,

		// Reads the same NVML per-device enumeration that gpu_count
		// measures, so a verdict about a device missing from that
		// enumeration names gpu_count as the cause.
		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalGPUPstate,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200", "B300", "H100", "A100", "VR"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_gpu_pstate": {
				Value:     int64(0),
				ValueType: health.ThresholdInt64,
				// NVML exposes P0 through P15.
				ValueMin:    int64(0),
				ValueMax:    int64(15),
				Category:    health.DefaultThreshold,
				Description: "Performance state every GPU must be in, as the numeric level N in P<N> (0 = P0, maximum performance). Override per fleet.",
				Unit:        "pstate",
			},
			// VR moves between P0 (busy) and P5 (idle) with load, and this
			// check runs continuously, so any state in between passes.
			"idle_gpu_pstate": {
				Value: int64(-1),
				PerArchitecture: map[string]any{
					"GB200": int64(-1),
					"GB300": int64(-1),
					"B200":  int64(-1),
					"B300":  int64(-1),
					"H100":  int64(-1),
					"A100":  int64(-1),
					"VR":    int64(5),
				},
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(-1),
				ValueMax:    int64(15),
				Category:    health.DefaultThreshold,
				Description: "Lowest-power state an idle GPU may drop to, as the numeric level N in P<N>, or -1 for none. When set, any state from expected_gpu_pstate to this one passes. 5 (P5) on VR, -1 on every other GPU. Override per fleet.",
				Unit:        "pstate",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "idle_gpu_pstate < 0 && gpu_pstate != expected_gpu_pstate",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: performance state P${gpu_pstate}, expected P${expected_gpu_pstate}.",
			},
			{
				Expression: "idle_gpu_pstate >= 0 && !((gpu_pstate >= expected_gpu_pstate && gpu_pstate <= idle_gpu_pstate) || (gpu_pstate >= idle_gpu_pstate && gpu_pstate <= expected_gpu_pstate))",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: performance state P${gpu_pstate}, expected P${expected_gpu_pstate} to P${idle_gpu_pstate}.",
			},
		},
	}
}
