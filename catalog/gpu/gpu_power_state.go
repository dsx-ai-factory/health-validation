// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPowerState returns the check definition that verifies each GPU is
// operating at the expected performance state (pstate).
//
// The check fires Critical when a GPU's pstate falls outside the range
// from expected_gpu_pstate (busy) to idle_gpu_pstate (idle). Each is the
// numeric level N in P<N> (0 = P0, maximum performance). Both are P0
// except on VR, which idles at P5.
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
				Value: int64(0),
				PerArchitecture: map[string]any{
					"GB200": int64(0),
					"GB300": int64(0),
					"B200":  int64(0),
					"B300":  int64(0),
					"H100":  int64(0),
					"A100":  int64(0),
					"VR":    int64(5),
				},
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(0),
				ValueMax:    int64(15),
				Category:    health.DefaultThreshold,
				Description: "Performance state an idle GPU settles in, as the numeric level N in P<N>. Any state from expected_gpu_pstate to this one passes. 5 (P5) on VR, 0 (P0) on every other GPU. Override per fleet.",
				Unit:        "pstate",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "!((gpu_pstate >= expected_gpu_pstate && gpu_pstate <= idle_gpu_pstate) || (gpu_pstate >= idle_gpu_pstate && gpu_pstate <= expected_gpu_pstate))",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: performance state P${gpu_pstate}, expected P${expected_gpu_pstate} to P${idle_gpu_pstate}.",
			},
		},
	}
}
