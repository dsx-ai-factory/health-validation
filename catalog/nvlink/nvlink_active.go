// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// NVLinkActive returns the check definition that verifies each GPU has
// no inactive NVLinks.
//
// The check fires Critical when a GPU's inactive_count exceeds
// max_inactive_nvlinks — typically a link-training or fabric fault that
// silently degrades multi-GPU collectives (NCCL stalls or falls back to
// PCIe). The budget is per GPU and defaults to zero, so every link a GPU
// exposes must be active.
func NVLinkActive() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_active",
		Name:        "NVLink Active Links",
		Description: "Verifies each GPU has no inactive NVLinks.",
		Category:    health.NVLink,

		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalInactiveCount,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200", "H100", "A100"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"max_inactive_nvlinks": {
				Value:       int64(0),
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(0),
				Category:    health.DefaultThreshold,
				Description: "Maximum inactive NVLinks tolerated per GPU before the GPU is considered failing.",
				Unit:        "count",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "inactive_count > max_inactive_nvlinks",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: ${inactive_count} inactive NVLinks (allowed: ${max_inactive_nvlinks}).",
			},
		},
	}
}
