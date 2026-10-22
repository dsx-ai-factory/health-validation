// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// PortState detects a port that is not carrying traffic: a logical state
// other than ACTIVE, or a physical state other than LinkUp.
//
// The two are separate rules because they separate causes. A port stuck in
// Init or Armed trained its link but was never brought up by the subnet
// manager, so the fabric — not the cable — is the suspect; a physical state
// of Polling or Disabled means the link never trained at all, which points
// at the cable, transceiver, or the switch port. A down port reports both.
//
// It applies to every port on a link layer in checked_link_layers: a port that
// is down is down whether it runs InfiniBand or RoCE. A fleet with uncabled
// RoCE ports drops Ethernet from that list rather than disabling the check.
//
// It consumes a [PortMeasurement] from the sysfs port sensor and evaluates one
// port per measurement. Both expected states are operator-overridable for
// fleets that run a different steady state.
func PortState() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "ib_port_state",
		Name:        "InfiniBand Port State",
		Description: "Verifies every port on a checked link layer (InfiniBand and RoCE by default) is in the expected logical state (ACTIVE) and physical link state (LinkUp). Fails CRITICAL per port that is not.",
		Category:    health.InfiniBand,

		RequiredSignals: []string{
			SignalIBDevice,
			SignalIBPort,
			SignalIBState,
			SignalIBPhysicalState,
			SignalIBLinkLayer,
		},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		Architectures:     []string{"GB200", "GB300"},

		ConditionScope:    health.ClusterScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Experimental,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"checked_link_layers": {
				Value:       []string{"InfiniBand", "Ethernet"},
				ValueType:   health.ThresholdStringList,
				Category:    health.DefaultThreshold,
				Description: "Link layers, as sysfs names them, whose ports this check evaluates; ports on any other link layer are not assessed. Drop \"Ethernet\" for a fleet whose RoCE ports are uncabled or run at their own speeds, rather than disabling the check.",
			},
			"expected_ib_state": {
				Value:       "ACTIVE",
				ValueType:   health.ThresholdString,
				Category:    health.DefaultThreshold,
				Description: "Logical port state every port must report, as sysfs names it (the text after the colon in /sys/class/infiniband/<dev>/ports/<n>/state). Override per fleet.",
				Unit:        "state",
			},
			"expected_ib_physical_state": {
				Value:       "LinkUp",
				ValueType:   health.ThresholdString,
				Category:    health.DefaultThreshold,
				Description: "Physical link state every port must report, as sysfs names it in phys_state. Override per fleet.",
				Unit:        "state",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "ib_link_layer in checked_link_layers && ib_state != expected_ib_state",
				Severity:   health.Critical,
				Message:    "Port ${ib_device}:${ib_port} (${ib_link_layer}): state ${ib_state}, expected ${expected_ib_state}.",
			},
			{
				Expression: "ib_link_layer in checked_link_layers && ib_physical_state != expected_ib_physical_state",
				Severity:   health.Critical,
				Message:    "Port ${ib_device}:${ib_port} (${ib_link_layer}): physical state ${ib_physical_state}, expected ${expected_ib_physical_state}.",
			},
		},
	}
}
