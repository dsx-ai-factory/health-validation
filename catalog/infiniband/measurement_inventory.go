// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// SignalPortRatesGbps is the inventory signal: every port's rate in Gb/s,
// keyed "<device>/<port>". It stays disjoint from the per-port signals so a
// check routes to one measurement type.
const SignalPortRatesGbps = "port_rates_gbps"

var portInventorySignals = []string{SignalPortRatesGbps}

// PortInventorySignals lists every signal a PortInventoryMeasurement carries.
// The returned slice is a copy.
func PortInventorySignals() []string { return slices.Clone(portInventorySignals) }

// PortInventoryMeasurement is the node-wide view of which RDMA ports exist
// and at what rate. It deliberately omits port state: a down port still
// enumerates, so presence and rate answer "does the node expose its rails"
// and leave "is each one healthy" to the per-port measurement.
//
// A port whose rate could not be read is absent from the map, so it never
// counts toward a rail total.
type PortInventoryMeasurement struct {
	RatesGbps map[string]float64 `json:"port_rates_gbps"`
	errors    []health.ErroredMeasurement
}

// ErroredPortInventory returns a PortInventoryMeasurement marked errored for
// a whole-source failure.
func ErroredPortInventory(err error) PortInventoryMeasurement {
	var m PortInventoryMeasurement
	m.AddError(err, portInventorySignals...)
	return m
}

// AddError appends one collection failure to the measurement.
func (m *PortInventoryMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (PortInventoryMeasurement) Category() health.Category { return health.InfiniBand }

// Errors implements [health.Measurement].
func (m PortInventoryMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// InventoryOf derives the node-wide view from per-port measurements, so both
// shapes come from one pass over the sensor's observations.
//
// A failure that names no device is a failure of the whole source and marks
// the inventory errored. A port or device whose rate could not be read is
// left out instead: that fails closed for a rail and is harmless for a
// storage port.
func InventoryOf(ports []PortMeasurement) PortInventoryMeasurement {
	inv := PortInventoryMeasurement{RatesGbps: make(map[string]float64, len(ports))}
	for _, p := range ports {
		rateFailed := false
		for _, e := range p.Errors() {
			if !slices.Contains(e.FailedSignals(), SignalIBRateGbps) {
				continue
			}
			rateFailed = true
			if p.Device == "" {
				inv.AddError(e.Err(), portInventorySignals...)
			}
		}
		if id := p.EntityID(); id != "" && p.Port > 0 && !rateFailed {
			inv.RatesGbps[id] = p.RateGbps
		}
	}
	return inv
}
