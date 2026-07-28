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

// NVLinkP2P returns the check definition that verifies each GPU has
// healthy peer-to-peer (P2P) NVLink connectivity to every other GPU.
//
// The check fires Critical when a GPU's unhealthy_p2p_peer_count exceeds
// max_unhealthy_p2p_peers — one or more peer GPUs it cannot reach over
// NVLink P2P. A peer that is not reachable silently degrades multi-GPU
// collectives (NCCL stalls or falls back to a slower path). The budget
// is per GPU and defaults to zero, so every peer must be reachable.
func NVLinkP2P() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_p2p",
		Name:        "NVLink P2P Connectivity",
		Description: "Verifies each GPU has healthy peer-to-peer NVLink connectivity to every other GPU.",
		Category:    health.NVLink,

		RequiredSignals: []string{
			SignalGPUID,
			SignalUnhealthyP2PPeerCount,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "H100", "H200", "A100"},
		Provenance:        health.NVIDIA,
		MaturityLevel:     "stable",
		RecommendedAction: health.Drain(health.DiagnosticActionContactSupport),

		Thresholds: map[string]health.ThresholdSpec{
			"max_unhealthy_p2p_peers": {
				Value:       int64(0),
				Category:    health.DefaultThreshold,
				Description: "Maximum peer GPUs unreachable over NVLink P2P tolerated per GPU before the GPU is considered failing.",
				Unit:        "count",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "unhealthy_p2p_peer_count > max_unhealthy_p2p_peers",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: ${unhealthy_p2p_peer_count} peer GPUs unreachable over NVLink P2P (allowed: ${max_unhealthy_p2p_peers}).",
			},
		},
	}
}
