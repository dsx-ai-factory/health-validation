// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// NVLinkCapabilities returns the check definition that verifies every
// NVLink on each GPU advertises the capabilities required for multi-GPU
// communication: peer-to-peer access, system-memory access, peer-to-peer
// and system-memory atomics, SLI, and link validity.
//
// Each capability is a per-GPU boolean, healthy when true. The NVML
// collector initializes every signal to true, clears one to false the
// moment a present link reports that capability unsupported, and
// attributes read failures via AddError. Choosing true as healthy also
// makes the zero value fail safe: an unpopulated measurement reads false
// and surfaces as Critical rather than silently passing.
//
// This check covers capabilities only. GPU count, NVLink count, and link
// activity are verified by gpu_count, nvlink_bandwidth_count_speed, and
// nvlink_active respectively.
//
// It declares a dependency on gpu_count so that a device missing from the
// enumeration gpu_count measures is named as the cause here rather than
// reported as an unexplained collection failure. Dependencies gate nothing.
func NVLinkCapabilities() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_capabilities",
		Name:        "NVLink Capabilities",
		Description: "Verifies every NVLink on each GPU reports the capabilities required for multi-GPU communication (P2P, system-memory access, P2P and system-memory atomics, SLI, and link validity).",
		Category:    health.NVLink,

		Dependencies: []string{"gpu_count"},

		RequiredSignals: []string{
			SignalGPUID,
			SignalAllLinksSupportP2P,
			SignalAllLinksSupportSysmemAccess,
			SignalAllLinksSupportP2PAtomics,
			SignalAllLinksSupportSysmemAtomics,
			SignalAllLinksSupportSLI,
			SignalAllLinksSupportLink,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200"},

		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.GenericDrain(),

		Rules: []health.Rule{
			{
				Expression: "all_links_support_p2p == false",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: one or more NVLinks do not support P2P.",
			},
			{
				Expression: "all_links_support_sysmem_access == false",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: one or more NVLinks do not support system memory access.",
			},
			{
				Expression: "all_links_support_p2p_atomics == false",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: one or more NVLinks do not support P2P atomics.",
			},
			{
				Expression: "all_links_support_sysmem_atomics == false",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: one or more NVLinks do not support system memory atomics.",
			},
			{
				Expression: "all_links_support_sli == false",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: one or more NVLinks do not support SLI.",
			},
			{
				Expression: "all_links_support_link == false",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: one or more NVLinks are not reported as supported (Link capability).",
			},
		},
	}
}
