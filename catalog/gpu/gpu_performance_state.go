// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPerformanceState returns the check definition that verifies each GPU's
// performance state (pstate) is within the accepted range.
//
// The check fires Critical when a GPU's pstate is outside the inclusive
// range from max_performance_pstate to min_performance_pstate. Each is the
// numeric level N in P<N>; following NVML, P0 is maximum performance, so the
// "max" bound is numerically the lower. Both are P0 except on VR, which
// idles at P5. min_performance_pstate is assumed to be >=
// max_performance_pstate; thresholds are not cross-validated, so an override
// that breaks this passes only at max_performance_pstate.
func GPUPerformanceState() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_performance_state",
		Name:        "GPU Performance State",
		Description: "Verifies each GPU is operating within the accepted performance state range.",
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
			"max_performance_pstate": {
				Value:     int64(0),
				ValueType: health.ThresholdInt64,
				// NVML exposes P0 through P15.
				ValueMin:    int64(0),
				ValueMax:    int64(15),
				Category:    health.DefaultThreshold,
				Description: "Highest-performance state a GPU may be in, as the numeric level N in P<N> (0 = P0, maximum performance), so the numerically lowest accepted level. Override per fleet.",
				Unit:        "pstate",
			},
			// VR moves between P0 (busy) and P5 (idle) with load, and this
			// check runs continuously, so any state in between passes.
			"min_performance_pstate": {
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
				Description: "Lowest-performance state a GPU may be in, as the numeric level N in P<N>, so the numerically highest accepted level; must be >= max_performance_pstate. Any state from max_performance_pstate to this one passes. 5 (P5) on VR, 0 (P0) on every other GPU. Override per fleet.",
				Unit:        "pstate",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "gpu_pstate != max_performance_pstate && !(gpu_pstate >= max_performance_pstate && gpu_pstate <= min_performance_pstate)",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: performance state P${gpu_pstate}, expected P${max_performance_pstate} to P${min_performance_pstate}.",
			},
		},
	}
}
