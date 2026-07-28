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

		RequiredSignals: []string{
			SignalGPUID,
			SignalInactiveCount,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "H100", "H200", "A100"},

		Provenance:        health.NVIDIA,
		MaturityLevel:     "stable",
		RecommendedAction: health.Drain(health.DiagnosticActionRetestHardware),

		Thresholds: map[string]health.ThresholdSpec{
			"max_inactive_nvlinks": {
				Value:       int64(0),
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
