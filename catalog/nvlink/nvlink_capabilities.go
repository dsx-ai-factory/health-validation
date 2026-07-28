/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package nvlink

import "github.com/NVIDIA/health-validation/catalog/health"

// NVLinkCapabilities returns the check definition that verifies every
// NVLink on each GPU advertises the capabilities required for multi-GPU
// communication: peer-to-peer access, system-memory access, peer-to-peer
// and system-memory atomics, SLI, and link validity.
//
// Each capability is a per-GPU boolean, healthy when true. The NVML
// collector initializes every signal to true, clears one to false the
// moment a present link reports that capability unsupported, and
// attributes read failures via AddError (it leaves the signals true when
// the GPU has no NVLink, since that case is owned by the link-count
// check). Choosing true as healthy also makes the zero value fail safe: a
// measurement that reaches the rule without the collector having
// populated it — a synthetic value, or the collector call being dropped —
// reads false and surfaces as Critical rather than silently passing.
//
// The check covers capabilities only. GPU count and per-GPU NVLink count
// are verified by the gpu_count and nvlink_bandwidth_count_speed checks,
// so they are intentionally not repeated here.
//
// It deliberately declares no dependency on gpu_count: the catalog
// evaluates a check only against measurements of its own Category, so a
// Node-category check cannot gate this NVLink-category one. A node with
// missing GPUs still fails gpu_count, so the node verdict is Critical
// regardless.
func NVLinkCapabilities() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_capabilities",
		Name:        "NVLink Capabilities",
		Description: "Verifies every NVLink on each GPU reports the capabilities required for multi-GPU communication (P2P, system-memory access, P2P and system-memory atomics, SLI, and link validity).",
		Category:    health.NVLink,

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
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200"},

		Provenance:        health.NVIDIA,
		MaturityLevel:     "stable",
		RecommendedAction: health.Drain(health.DiagnosticActionRetestHardware),

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
