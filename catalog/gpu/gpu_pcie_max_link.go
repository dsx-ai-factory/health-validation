// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUPCIeMaxLink verifies the maximum PCIe link reported for each GPU.
// The PCI sysfs maxima describe link capability, rather than the current
// negotiated state, which may change with power management.
func GPUPCIeMaxLink() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "gpu_pcie_max_link",
		Name:              "GPU PCIe Maximum Link",
		Description:       "Verifies each GPU's maximum PCIe link width and generation match the expected configuration.",
		Category:          health.GPU,
		Dependencies:      []string{"gpu_count"},
		RequiredSignals:   []string{SignalGPUID, SignalPCIeMaxLinkWidth, SignalPCIeMaxLinkGeneration},
		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200", "H100", "H200", "A100", "VR200"},
		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),
		Thresholds: map[string]health.ThresholdSpec{
			"expected_pcie_max_link_width": {
				Value:       int64(16),
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(1),
				ValueMax:    int64(32),
				Category:    health.DefaultThreshold,
				Unit:        "lanes",
				Description: "Expected maximum PCIe link width per GPU. Override for the deployed system configuration.",
			},
			"expected_pcie_max_link_generation": {
				PerArchitecture: map[string]any{"A100": int64(4), "H100": int64(5), "H200": int64(5), "B200": int64(5), "GB200": int64(5), "GB300": int64(6), "VR200": int64(6)},
				ValueType:       health.ThresholdInt64, ValueMin: int64(1),
				Category:    health.DefaultThreshold,
				Unit:        "generation",
				Description: "Expected maximum PCIe generation per GPU, expressed as an integer (5 means Gen5). Override for the deployed system configuration.",
			},
		},
		Rules: []health.Rule{
			{
				Expression: "pcie_max_link_width != expected_pcie_max_link_width",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: maximum PCIe link width x${pcie_max_link_width}, expected x${expected_pcie_max_link_width}.",
			},
			{
				Expression: "pcie_max_link_generation != expected_pcie_max_link_generation",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: maximum PCIe generation ${pcie_max_link_generation}, expected ${expected_pcie_max_link_generation}.",
			},
		},
	}
}
