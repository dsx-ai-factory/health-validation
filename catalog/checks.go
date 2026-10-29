// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// builtinChecks returns all NVIDIA check definitions shipped with
// the catalog. Add new checks here.
func builtinChecks() []health.CheckDefinition {
	checks := []health.CheckDefinition{
		cpu.CPUCount(),
		cpu.NUMANodeCount(),
		gpu.GPUAveragePowerDraw(),
		gpu.GPUPowerLimit(),
		gpu.GPUPowerState(),
		gpu.GPUSensorThrottle(),
		gpu.GPUComputeApps(),
		node.GPUCount(),
		pcie.RDMATopo(),
		infiniband.RDMALinkFlapFatal(),
		nvlink.NVLinkActive(),
		nvlink.NVLinkFabricHealth(),
		nvlink.NVLinkFabric(),
		nvlink.NVLinkBandwidthCountSpeed(),
		nvlink.NVLinkCapabilities(),
		nvlink.NVLinkP2P(),
	}
	// One CRITICAL check per critical Xid code, plus architecture-scoped
	// WARNING checks and the Xid 154 INFO check.
	checks = append(checks, xid.Checks()...)
	return checks
}
