// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	// NVLinkState is the raw nvmlEnableState_t of one link: 0 disabled, 1 enabled.
	NVLinkState                      = collect.NewSignal("gpu.nvlink.state", "1")
	NVLinkEndpointReplayErrorCount   = collect.NewSignal("gpu.nvlink.endpoint.error.dl_replay.count", "1")
	NVLinkEndpointRecoveryErrorCount = collect.NewSignal("gpu.nvlink.endpoint.error.dl_recovery.count", "1")
	NVLinkEndpointCRCFlitErrorCount  = collect.NewSignal("gpu.nvlink.endpoint.error.dl_crc_flit.count", "1")
	NVLinkFabricState                = collect.NewSignal("gpu.nvlink.fabric.state", "1")
	NVLinkFabricStatus               = collect.NewSignal("gpu.nvlink.fabric.status", "1")
	NVLinkFabricCliqueID             = collect.NewSignal("gpu.nvlink.fabric.clique_id", "1")
	NVLinkFabricClusterUUID          = collect.NewSignal("gpu.nvlink.fabric.cluster_uuid", "")
	NVLinkFabricHealthMask           = collect.NewSignal("gpu.nvlink.fabric.health_mask", "1")
	NVLinkFabricHealthSummary        = collect.NewSignal("gpu.nvlink.fabric.health_summary", "1")
	NVLinkLinkCount                  = collect.NewSignal("gpu.nvlink.link.count", "1")
	NVLinkSpeedCommon                = collect.NewSignal("gpu.nvlink.speed.common", "megabytes_per_second")
	NVLinkEndpointSpeed              = collect.NewSignal("gpu.nvlink.endpoint.speed", "megabytes_per_second")

	NVLinkEndpointCapabilityP2PSupported  = collect.NewSignal("gpu.nvlink.endpoint.capability.p2p_supported", "1")
	NVLinkEndpointCapabilitySysmemAccess  = collect.NewSignal("gpu.nvlink.endpoint.capability.sysmem_access", "1")
	NVLinkEndpointCapabilityP2PAtomics    = collect.NewSignal("gpu.nvlink.endpoint.capability.p2p_atomics", "1")
	NVLinkEndpointCapabilitySysmemAtomics = collect.NewSignal("gpu.nvlink.endpoint.capability.sysmem_atomics", "1")
	NVLinkEndpointCapabilitySLIBridge     = collect.NewSignal("gpu.nvlink.endpoint.capability.sli_bridge", "1")
	NVLinkEndpointCapabilityValid         = collect.NewSignal("gpu.nvlink.endpoint.capability.valid", "1")

	// NVLinkP2PStatus is the raw nvmlGpuP2PStatus_t of an ordered GPU pair over
	// NVLink: 0 is OK.
	NVLinkP2PStatus = collect.NewSignal("gpu.nvlink.p2p.status", "1")
)

// PairSignals returns signals whose values describe an ordered source/peer GPU pair.
func PairSignals() collect.Signals {
	return collect.Signals{NVLinkP2PStatus}
}

// EndpointCapabilitySignals returns the raw per-link NVLink capability results:
// nonzero means the link supports the capability.
func EndpointCapabilitySignals() collect.Signals {
	return collect.Signals{NVLinkEndpointCapabilityP2PSupported, NVLinkEndpointCapabilitySysmemAccess, NVLinkEndpointCapabilityP2PAtomics, NVLinkEndpointCapabilitySysmemAtomics, NVLinkEndpointCapabilitySLIBridge, NVLinkEndpointCapabilityValid}
}

// InterconnectAdditions returns new definitions without existing canonical signals.
func InterconnectAdditions() collect.Signals {
	return append(EndpointSignals(), NVLinkFabricState, NVLinkFabricStatus, NVLinkFabricCliqueID, NVLinkFabricClusterUUID, NVLinkFabricHealthMask, NVLinkFabricHealthSummary, NVLinkLinkCount, NVLinkSpeedCommon, NVLinkP2PStatus)
}

// Interconnect returns per-endpoint NVLink state, errors, speed and
// capabilities, the GPU's link count and common speed, per-pair P2P status,
// plus raw fabric fields.
// No throughput signal is included: these native reads do not measure traffic.
func Interconnect() collect.Signals {
	return append(InterconnectAdditions(), NVLinkFabricBandwidthDegraded, NVLinkFabricRouteRecoveryActive, NVLinkFabricRouteUnhealthy, NVLinkFabricAccessTimeoutRecovery)
}

// EndpointSignals returns signals whose values identify one local NVLink endpoint.
// The GPU-wide DCGM aggregate counters use separate canonical definitions.
func EndpointSignals() collect.Signals {
	return append(collect.Signals{NVLinkState, NVLinkEndpointReplayErrorCount, NVLinkEndpointRecoveryErrorCount, NVLinkEndpointCRCFlitErrorCount, NVLinkEndpointSpeed}, EndpointCapabilitySignals()...)
}
