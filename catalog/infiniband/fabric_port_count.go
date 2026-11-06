// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// RDMAFabricPortCount verifies that the node exposes its GPU fabric rails: at
// least expected_fabric_ports RDMA ports at fabric rate are present.
//
// It counts presence and rate, not state, the way gpu_count counts GPUs and
// leaves per-GPU configuration to other checks. A rail that is down still
// enumerates at its configured rate, so [PortState] reports it by name; this
// check catches what a per-port check cannot see: a rail missing from sysfs,
// or one trained below fabric rate and so no longer a fabric port.
func RDMAFabricPortCount() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "rdma_fabric_port_count",
		Name:        "RDMA Fabric Port Count",
		Description: "Verifies that at least expected_fabric_ports RDMA ports (InfiniBand or RoCE) at a rate of at least fabric_port_rate_gbps are present. Catches GPU fabric rails that are missing or trained below fabric rate.",
		Category:    health.InfiniBand,

		RequiredSignals: []string{SignalPortRatesGbps},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		// Validated on HGX B200 only. Other architectures need their own
		// rail count before they are listed.
		Architectures: []string{"B200"},

		ConditionScope: health.NodeScope,
		Provenance:     health.Community,
		MaturityLevel:  health.Experimental,
		// A GPU reset cannot bring a NIC port back. A restart reapplies the
		// node's network configuration; beyond that it is cabling, optics,
		// the switch port, or provisioning.
		RecommendedAction: health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_fabric_ports": {
				Value:     int64(8),
				ValueType: health.ThresholdInt64,
				// At 0 the rule can never fire: a count is always >= 0.
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Fabric ports the node must expose: one per GPU on HGX B200 (8). Override per fleet for other rail layouts.",
				Unit:        "count",
			},
			"fabric_port_rate_gbps": fabricRateThreshold(),
		},

		Rules: []health.Rule{
			{
				Expression: "size(port_rates_gbps.filter(p, port_rates_gbps[p] >= fabric_port_rate_gbps)) < expected_fabric_ports",
				Severity:   health.Critical,
				Message:    "Fewer than ${expected_fabric_ports} RDMA fabric ports (rate >= ${fabric_port_rate_gbps} Gb/s) are present. A port absent from port_rates_gbps is missing; one below the rate has trained down.",
			},
		},
	}
}
