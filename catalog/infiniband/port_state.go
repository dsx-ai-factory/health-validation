// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// PortState detects a fabric port that is not carrying traffic: a logical
// state other than ACTIVE, or a physical state other than LinkUp.
//
// The two are separate rules because they separate causes. A port stuck in
// Init or Armed trained its link but was never brought up by the subnet
// manager, so the fabric — not the cable — is the suspect; a physical state
// of Polling or Disabled means the link never trained at all, which points
// at the cable, transceiver, or the switch port. A down port reports both.
//
// It applies to fabric ports, those at or above fabric_port_rate_gbps,
// whatever their link layer: a rail that is down is down whether it runs
// InfiniBand or RoCE. Front-end and storage ports, which may legitimately be
// uncabled, are outside that class. Which rails a node must have is
// [RDMAFabricPortCount]'s question; a rail that is absent, or trained below
// the rate, is not a fabric port here.
//
// It consumes a [PortMeasurement] from the sysfs port sensor and evaluates one
// port per measurement. Both expected states are operator-overridable for
// fleets that run a different steady state.
func PortState() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "ib_port_state",
		Name:        "InfiniBand Port State",
		Description: "Verifies every fabric port (rate at or above fabric_port_rate_gbps, InfiniBand and RoCE alike) is in the expected logical state (ACTIVE) and physical link state (LinkUp). Fails CRITICAL per port that is not.",
		Category:    health.InfiniBand,

		RequiredSignals: []string{
			SignalIBDevice,
			SignalIBPort,
			SignalIBState,
			SignalIBPhysicalState,
			SignalIBRateGbps,
			SignalIBLinkLayer,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300", "B200"},

		ConditionScope:    health.ClusterScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Experimental,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"fabric_port_rate_gbps": fabricRateThreshold(),
			"expected_ib_state": {
				Value:       "ACTIVE",
				ValueType:   health.ThresholdString,
				Category:    health.DefaultThreshold,
				Description: "Logical port state every fabric port must report, as sysfs names it (the text after the colon in /sys/class/infiniband/<dev>/ports/<n>/state). Override per fleet.",
				Unit:        "state",
			},
			"expected_ib_physical_state": {
				Value:       "LinkUp",
				ValueType:   health.ThresholdString,
				Category:    health.DefaultThreshold,
				Description: "Physical link state every fabric port must report, as sysfs names it in phys_state. Override per fleet.",
				Unit:        "state",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "ib_rate_gbps >= fabric_port_rate_gbps && ib_state != expected_ib_state",
				Severity:   health.Critical,
				Message:    "Port ${ib_device}:${ib_port} (${ib_link_layer}): state ${ib_state}, expected ${expected_ib_state}.",
			},
			{
				Expression: "ib_rate_gbps >= fabric_port_rate_gbps && ib_physical_state != expected_ib_physical_state",
				Severity:   health.Critical,
				Message:    "Port ${ib_device}:${ib_port} (${ib_link_layer}): physical state ${ib_physical_state}, expected ${expected_ib_physical_state}.",
			},
		},
	}
}
