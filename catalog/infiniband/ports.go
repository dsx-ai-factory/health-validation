// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Port signal field names. Each is a map keyed by "<device>/<port>" (for
// example "mlx5_0/1"), so one node-wide measurement carries every RDMA port
// and a rule can count across them.
const (
	SignalPortStates         = "port_states"
	SignalPortPhysicalStates = "port_physical_states"
	SignalPortRatesGbps      = "port_rates_gbps"
	SignalPortLinkLayers     = "port_link_layers"
)

// Port state numbers as sysfs reports them in ports/<n>/state and
// ports/<n>/phys_state.
const (
	PortStateActive         = 4
	PortPhysicalStateLinkUp = 5
)

var portSignals = []string{
	SignalPortStates,
	SignalPortPhysicalStates,
	SignalPortRatesGbps,
	SignalPortLinkLayers,
}

// PortSignals lists every signal a PortMeasurement carries.
// The returned slice is a copy.
func PortSignals() []string { return slices.Clone(portSignals) }

// PortMeasurement is the node-wide view of RDMA ports read from sysfs: one
// entry per port in each map, InfiniBand and RoCE (Ethernet link layer)
// alike. A port missing from a map had no value for that signal, which a
// rule must treat as "not known to be healthy".
//
// RatesGbps is the port's reported rate. sysfs reports the configured rate
// even for a port that is down, so a down fabric port still identifies
// itself as one.
type PortMeasurement struct {
	States         map[string]int64   `json:"port_states"`
	PhysicalStates map[string]int64   `json:"port_physical_states"`
	RatesGbps      map[string]float64 `json:"port_rates_gbps"`
	LinkLayers     map[string]string  `json:"port_link_layers"`
	errors         []health.ErroredMeasurement
}

// ErroredPorts returns a PortMeasurement marked errored for a whole-source
// failure. An unattributed failure applies to every port signal.
func ErroredPorts(err error, failedSignals ...string) PortMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = portSignals
	}
	var m PortMeasurement
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection failure to the measurement.
func (m *PortMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (PortMeasurement) Category() health.Category { return health.InfiniBand }

// Errors implements [health.Measurement].
func (m PortMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// RDMAFabricPortsActive verifies that the node's GPU fabric rails are up: at
// least expected_active_fabric_ports RDMA ports at fabric speed are ACTIVE
// with a LinkUp physical state.
//
// A fabric port is one whose rate is at least fabric_port_rate_gbps. That
// separates the per-GPU east-west NICs from front-end and storage ports on
// the same node without naming devices, and a rail that trained down to a
// lower rate stops counting, so it fails too. RoCE ports count the same as
// InfiniBand ones.
//
// The failure this exists for: a port that never came up (for example left
// administratively down by node provisioning) never flaps, so
// [RDMALinkFlapFatal] passes while the node runs on fewer rails.
func RDMAFabricPortsActive() health.CheckDefinition {
	return health.CheckDefinition{
		ID:          "rdma_fabric_ports_active",
		Name:        "RDMA Fabric Ports Active",
		Description: "Verifies that at least expected_active_fabric_ports RDMA ports (InfiniBand or RoCE) at a rate of at least fabric_port_rate_gbps are ACTIVE and LinkUp. Catches GPU fabric rails that are down or never came up, which link-flap detection cannot see.",
		Category:    health.InfiniBand,

		RequiredSignals: []string{SignalPortStates, SignalPortPhysicalStates, SignalPortRatesGbps},

		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Continuous,
		// Validated on HGX B200 only. Other architectures need their own
		// rail count and rate before they are listed.
		Architectures: []string{"B200"},

		ConditionScope: health.NodeScope,
		Provenance:     health.Community,
		MaturityLevel:  health.Experimental,
		// A GPU reset cannot bring a NIC port up. A restart reapplies the
		// node's network configuration; beyond that it is cabling, optics,
		// the switch port, or provisioning.
		RecommendedAction: health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired),

		Thresholds: map[string]health.ThresholdSpec{
			"expected_active_fabric_ports": {
				Value:     int64(8),
				ValueType: health.ThresholdInt64,
				// At 0 the rule can never fire: a count is always >= 0.
				ValueMin:    int64(1),
				Category:    health.DefaultThreshold,
				Description: "Fabric ports that must be ACTIVE and LinkUp: one per GPU on HGX B200 (8). Override per fleet for other rail layouts.",
				Unit:        "count",
			},
			"fabric_port_rate_gbps": {
				Value:       float64(400),
				ValueType:   health.ThresholdFloat64,
				ValueMin:    float64(1),
				Category:    health.DefaultThreshold,
				Description: "Minimum port rate, in Gb/s, for a port to count as a fabric rail: 400 for NDR / 400GbE rails on HGX B200. Ports below it, such as front-end or storage NICs, are ignored.",
				Unit:        "Gb/s",
			},
		},

		Rules: []health.Rule{
			{
				Expression: "size(port_rates_gbps.filter(p, port_rates_gbps[p] >= fabric_port_rate_gbps && p in port_states && port_states[p] == 4 && p in port_physical_states && port_physical_states[p] == 5)) < expected_active_fabric_ports",
				Severity:   health.Critical,
				Message:    "Fewer than ${expected_active_fabric_ports} RDMA fabric ports (rate >= ${fabric_port_rate_gbps} Gb/s) are ACTIVE and LinkUp. See port_states, port_physical_states and port_rates_gbps for the ports that are not.",
			},
		},
	}
}
