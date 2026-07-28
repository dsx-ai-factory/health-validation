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

// Package node defines health check definitions that operate on
// host-wide signals (GPU count, driver version, etc.) rather than
// per-device measurements.
package node

import "github.com/NVIDIA/health-validation/catalog/health"

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
// count. Defaults below target GB200 (4 Blackwell GPUs per node);
// extend the Architectures list and adjust expected_gpu_count via
// [catalog.WithOverrides] for other architectures, or register
// an architecture-specific variant.
func GPUCount() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "gpu_count",
		Name:        "GPU Count",
		Description: "Verifies a node enumerated the expected number of GPUs for its architecture.",
		Category:    health.Node,

		RequiredSignals: []string{SignalGPUCount},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300"},

		Provenance:        health.NVIDIA,
		MaturityLevel:     "stable",
		RecommendedAction: health.Drain(health.DiagnosticActionReboot),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_gpu_count": {
				Value:       int64(4),
				Category:    health.DefaultThreshold,
				Description: "Expected number of GPUs on a node. Override per fleet.",
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
