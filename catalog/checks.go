package catalog

import (
	"github.com/NVIDIA/health-validation/catalog/cpu"
	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/infiniband"
	"github.com/NVIDIA/health-validation/catalog/node"
	"github.com/NVIDIA/health-validation/catalog/nvlink"
	"github.com/NVIDIA/health-validation/catalog/xid"
)

// builtinChecks returns all NVIDIA check definitions shipped with
// the catalog. Add new checks here.
func builtinChecks() []health.CheckDefinition {
	checks := []health.CheckDefinition{
		cpu.CPUCount(),
		cpu.NUMANodeCount(),
		gpu.GPUPowerState(),
		infiniband.IBDeviceInventory(),
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
