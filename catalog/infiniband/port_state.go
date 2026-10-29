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
// It applies to every port the sensor reports, whatever its link layer: a
// port that is down is down whether it runs InfiniBand or RoCE. A fleet with
// uncabled RoCE ports lists Ethernet in excluded_link_layers rather than
// disabling the check. An exclusion list fails safe: a misspelled entry
// checks more ports than intended instead of letting a down port pass.
//
// It consumes a [PortMeasurement] from the sysfs port sensor and evaluates one
// port per measurement. Both expected states are operator-overridable for
// fleets that run a different steady state.
func PortState() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "ib_port_state",
		Name:        "InfiniBand Port State",
		Description: "Verifies every port (InfiniBand and RoCE alike, unless its link layer is excluded) is in the expected logical state (ACTIVE) and physical link state (LinkUp). Fails CRITICAL per port that is not.",
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
		Architectures:     []string{"GB200", "GB300", "B200"},

		ConditionScope:    health.ClusterScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Experimental,
		RecommendedAction: health.GenericDrain(),

		Thresholds: map[string]health.ThresholdSpec{
			"excluded_link_layers": {
				Value:       []string{},
				ValueType:   health.ThresholdStringList,
				Category:    health.DefaultThreshold,
				Description: "Link layers, as sysfs names them (\"InfiniBand\", \"Ethernet\"), whose ports this check does not assess. Empty by default, so every port is checked. Add \"Ethernet\" for a fleet whose RoCE ports are uncabled, rather than disabling the check. A misspelled entry excludes nothing.",
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
				Expression: "!(ib_link_layer in excluded_link_layers) && ib_state != expected_ib_state",
				Severity:   health.Critical,
				Message:    "Port ${ib_device}:${ib_port} (${ib_link_layer}): state ${ib_state}, expected ${expected_ib_state}.",
			},
			{
				Expression: "!(ib_link_layer in excluded_link_layers) && ib_physical_state != expected_ib_physical_state",
				Severity:   health.Critical,
				Message:    "Port ${ib_device}:${ib_port} (${ib_link_layer}): physical state ${ib_physical_state}, expected ${expected_ib_physical_state}.",
			},
		},
	}
}
