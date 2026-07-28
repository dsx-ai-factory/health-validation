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

// NVLinkFabric returns the check definition for NVLink fabric
// registration.
//
// On GB200 a GPU must register with the shared NVLink fabric before it
// can route traffic to its peers; a GPU that never completes
// registration, or completes it with a failure status, is cut off from
// multi-GPU NVLink collectives. Both conditions are collected per GPU
// from the same fabric-info query that feeds [NVLinkFabricHealth].
//
// The check models one severity tier:
//   - CRITICAL: fabric registration has not completed, or completed with
//     a failure status.
//   - PASS: registration completed successfully.
func NVLinkFabric() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "nvlink_fabric",
		Name:        "NVLink Fabric Registration",
		Description: "Verifies each GPU has completed NVLink fabric registration with a successful status. Incomplete or failed registration cuts the GPU off from multi-GPU NVLink communication.",
		Category:    health.NVLink,

		RequiredSignals: []string{
			SignalGPUID,
			SignalFabricRegistrationIncomplete,
			SignalFabricRegistrationFailed,
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
				Expression: "fabric_registration_incomplete == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric registration has not completed.",
			},
			{
				Expression: "fabric_registration_failed == true",
				Severity:   health.Critical,
				Message:    "GPU ${gpu_id}: NVLink fabric registration completed with a failure status.",
			},
		},
	}
}
