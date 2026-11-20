// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// linkFlapSinceUnknown labels a link-flap failure whose window is unknown,
// as observations does.
const linkFlapSinceUnknown = "boot"

// InfiniBand is the category's one built-in adapter. It builds the stock
// CLI's link-flap measurement and the port measurements from the collect
// sysfs port sensor.
func InfiniBand() *domainAdapter {
	a := newDomainAdapter(health.InfiniBand, erroredInfiniBand)
	a.complete = func(measurements []health.Measurement) []health.Measurement {
		return completePorts(completeLinkFlap(measurements))
	}
	return a
}

// erroredInfiniBand fails every measurement type of the category, so a
// domain-wide failure such as a cycle timeout fails each type's checks
// rather than leaving them without a measurement to skip.
func erroredInfiniBand(err error) []health.Measurement {
	return append([]health.Measurement{
		infiniband.ErroredLinkFlap(linkFlapSinceUnknown, err, infiniband.MeasurementSignals()...),
	}, erroredPorts(err)...)
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
