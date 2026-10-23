// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package node defines health check definitions that operate on
// host-wide signals (GPU count, driver version, etc.) rather than
// per-device measurements.
package node

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// GPUCount returns the check definition that verifies a node enumerated
// the expected number of GPUs for its architecture.
//
// The check fires Critical when the observed gpu_count does not match
// the expected count — typically a hardware-detection problem (PCIe
// link dead at POST, missing FRU, etc.) that warrants taking the node
// out of service for physical inspection. The expected count is an
// adjustable threshold, so operators tune it per fleet.
//
// The check is scoped to architectures with a known canonical GPU
// count: 4 per node on GB200 and GB300 (one Grace Blackwell compute
// tray), 8 per node on H100, H200 and B200 (HGX/DGX baseboards). A fleet
// with a different SKU overrides expected_gpu_count via
// [catalog.WithOverrides]; other architectures need an entry here
// before the check runs on them.
func GPUCount() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_count",
		Name:        "GPU Count",
		Description: "Verifies a node enumerated the expected number of GPUs for its architecture.",
		Category:    health.Node,

		RequiredSignals: []string{SignalGPUCount},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_gpu_count": {
				Value: int64(8),
				PerArchitecture: map[string]any{
					"GB200": int64(4),
					"GB300": int64(4),
					"B200":  int64(8),
				},
				ValueType:   health.ThresholdInt64,
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Expected number of GPUs on a node: 4 on GB200/GB300, 8 on B200. Override per fleet.",
				Unit:        "count",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "gpu_count != expected_gpu_count",
				Severity:   health.Critical,
				Message:    "Expected ${expected_gpu_count} GPUs, found ${gpu_count}.",
			},
		},
	}
}
