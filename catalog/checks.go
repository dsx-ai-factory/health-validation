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

package catalog

import (
	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
	"github.com/NVIDIA/health-validation/catalog/xid"
)

// builtinChecks returns all NVIDIA check definitions shipped with
// the catalog. Add new checks here.
func builtinChecks() []health.CheckDefinition {
	checks := []health.CheckDefinition{
		gpu.GPUPowerState(),
		node.GPUCount(),
		nvlink.NVLinkActive(),
		nvlink.NVLinkFabricHealth(),
		nvlink.NVLinkFabric(),
		nvlink.NVLinkBandwidthCountSpeed(),
		nvlink.NVLinkCapabilities(),
		nvlink.NVLinkP2P(),
	}
	// One CRITICAL check per critical Xid code, plus a consolidated
	// WARNING check and the Xid 154 INFO check.
	checks = append(checks, xid.Checks()...)
	return checks
}
