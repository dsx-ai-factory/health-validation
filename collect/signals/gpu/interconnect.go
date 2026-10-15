// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/collect"

var (
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
)

// InterconnectAdditions returns new definitions without existing canonical signals.
func InterconnectAdditions() collect.Signals {
	return append(EndpointSignals(), NVLinkFabricState, NVLinkFabricStatus, NVLinkFabricCliqueID, NVLinkFabricClusterUUID, NVLinkFabricHealthMask, NVLinkFabricHealthSummary)
}

// Interconnect returns per-endpoint NVLink state and errors, plus raw fabric fields.
// No throughput signal is included: these native reads do not measure traffic.
func Interconnect() collect.Signals {
	return append(InterconnectAdditions(), NVLinkFabricBandwidthDegraded, NVLinkFabricRouteRecoveryActive, NVLinkFabricRouteUnhealthy, NVLinkFabricAccessTimeoutRecovery)
}

// EndpointSignals returns signals whose values identify one local NVLink endpoint.
// The GPU-wide DCGM aggregate counters use separate canonical definitions.
func EndpointSignals() collect.Signals {
	return collect.Signals{NVLinkState, NVLinkEndpointReplayErrorCount, NVLinkEndpointRecoveryErrorCount, NVLinkEndpointCRCFlitErrorCount}
}
