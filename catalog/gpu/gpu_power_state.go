// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPowerState returns the check definition that verifies each GPU is
// operating at the expected performance state (pstate).
//
// The check fires Critical when a GPU's pstate differs from
// expected_gpu_pstate. expected_gpu_pstate is the numeric level N in
// P<N> (0 = P0, maximum performance).
func GPUPowerState() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_power_state",
		Name:        "GPU Power State",
		Description: "Verifies each GPU is operating at the expected performance state.",
		Category:    health.GPU,

		RequiredSignals: []string{
			SignalGPUID,
			SignalGPUPstate,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "H100", "A100"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_gpu_pstate": {
				Value:       int64(0),
				Category:    health.DefaultThreshold,
				Description: "Performance state every GPU must be in, as the numeric level N in P<N> (0 = P0, maximum performance). Override per fleet.",
				Unit:        "pstate",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "gpu_pstate != expected_gpu_pstate",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: performance state P${gpu_pstate}, expected P${expected_gpu_pstate}.",
			},
		},
	}
}
