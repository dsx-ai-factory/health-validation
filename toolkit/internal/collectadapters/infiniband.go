// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"maps"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

// linkFlapSinceUnknown labels a link-flap failure whose window is unknown,
// as observations does.
const linkFlapSinceUnknown = "boot"

// InfiniBand is the category's one built-in adapter. It replaces
// toolkit.RDMALinkFlapSensor for the stock CLI's link-flap measurement and
// feeds infiniband.PortMeasurement from the collect sysfs port sensor, plus the
// node-wide infiniband.PortInventoryMeasurement derived from those ports.
func InfiniBand() *domainAdapter {
	a := newDomainAdapter(health.InfiniBand, erroredInfiniBand)
	feedInventory(a)
	a.complete = func(measurements []health.Measurement) []health.Measurement {
		return completeInventory(completePorts(completeLinkFlap(measurements)))
	}
	return a
}

// InfiniBandPorts adapts only the port measurements, for an App whose
// link-flap measurement still comes from the legacy toolkit sensor: failing
// the link-flap type here as well would give its check two measurements.
func InfiniBandPorts() *domainAdapter {
	a := newDomainAdapter(health.InfiniBand, erroredPortTypes)
	feedInventory(a)
	linkFlap := ibsignals.LinkFlapSignals().IDs()
	maps.DeleteFunc(a.feeds, func(id string, _ []string) bool { return slices.Contains(linkFlap, id) })
	a.complete = func(measurements []health.Measurement) []health.Measurement {
		return completeInventory(completePorts(measurements))
	}
	return a
}

// feedInventory also maps the rate onto the inventory's signal, so signal
// narrowing requests it when only the inventory's check will run.
func feedInventory(a *domainAdapter) {
	rate := ibsignals.RateGbps.ID()
	a.feeds[rate] = slices.Concat(a.feeds[rate], []string{infiniband.SignalPortRatesGbps})
}

// erroredInfiniBand fails every measurement type of the category, so a
// domain-wide failure such as a cycle timeout fails each type's checks
// rather than leaving them without a measurement to skip.
func erroredInfiniBand(err error) []health.Measurement {
	return append([]health.Measurement{
		infiniband.ErroredLinkFlap(linkFlapSinceUnknown, err, infiniband.MeasurementSignals()...),
	}, erroredPortTypes(err)...)
}

// erroredPortTypes fails both port measurement types: the per-port one and the
// node-wide inventory.
func erroredPortTypes(err error) []health.Measurement {
	return append(erroredPorts(err), infiniband.ErroredPortInventory(err))
}

// erroredPorts is a failure to read any port. It names no device, so it is
// keyed by the adapter.
func erroredPorts(err error) []health.Measurement {
	m := infiniband.ErroredPort("", 0, err, infiniband.PortMeasurementSignals...)
	m.SetEntityKey(health.SourceEntity(adapterSource(health.InfiniBand)))
	return []health.Measurement{m}
}

// completeLinkFlap fails the link-flap measurement when the observations
// held none: a source that emitted neither a scan marker nor a collection
// error has not shown the window is clean. Neutral absence, such as a host
// without InfiniBand ports, never applies to this type.
func completeLinkFlap(measurements []health.Measurement) []health.Measurement {
	if slices.ContainsFunc(measurements, func(m health.Measurement) bool {
		_, ok := m.(infiniband.LinkFlapMeasurement)
		return ok
	}) {
		return measurements
	}
	return append(measurements, infiniband.ErroredLinkFlap(linkFlapSinceUnknown,
		errors.New("no link-flap scan marker or collection error was collected"), infiniband.MeasurementSignals()...))
}

// completePorts fails the port measurement when the observations held no
// port: a missing driver leaves sysfs as empty as a host without InfiniBand
// does, so absence cannot be read as "nothing to check". Deployments
// without InfiniBand disable the port check instead.
func completePorts(measurements []health.Measurement) []health.Measurement {
	if slices.ContainsFunc(measurements, func(m health.Measurement) bool {
		_, ok := m.(infiniband.PortMeasurement)
		return ok
	}) {
		return measurements
	}
	return append(measurements, erroredPorts(errors.New("no InfiniBand port observations were collected"))...)
}

// completeInventory adds the node-wide inventory, derived from the port
// measurements so both shapes come from one pass over the observations. It
// runs after completePorts, so a node with no ports yields an errored inventory.
func completeInventory(measurements []health.Measurement) []health.Measurement {
	var ports []infiniband.PortMeasurement
	for _, m := range measurements {
		if p, ok := m.(infiniband.PortMeasurement); ok {
			ports = append(ports, p)
		}
	}
	return append(measurements, infiniband.InventoryOf(ports))
}
