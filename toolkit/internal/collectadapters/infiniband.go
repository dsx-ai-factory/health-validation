// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

// linkFlapSinceUnknown labels a link-flap failure whose window is unknown,
// as observations does.
const linkFlapSinceUnknown = "boot"

// InfiniBand is the category's one built-in adapter. It replaces
// toolkit.RDMALinkFlapSensor for the stock CLI's link-flap measurement.
//
// The adapter is per entity (nodeScoped is false), so a per-port type can
// share it, but infiniband.LinkFlapMeasurement is a whole-node aggregate:
// its transitions, scan marker, and source-scoped failures group into one
// measurement however many sources produce them.
func InfiniBand() *domainAdapter {
	a := newDomainAdapter(health.InfiniBand, erroredInfiniBand, false)
	a.nodeSignals = make(map[string]bool)
	for _, id := range ibsignals.LinkFlapSignals().IDs() {
		a.nodeSignals[id] = true
	}
	a.complete = completeLinkFlap
	return a
}

// erroredInfiniBand fails every measurement type of the category, so a
// domain-wide failure such as a cycle timeout fails each type's checks
// rather than leaving them without a measurement to skip.
func erroredInfiniBand(err error) []health.Measurement {
	return []health.Measurement{
		infiniband.ErroredLinkFlap(linkFlapSinceUnknown, err, infiniband.MeasurementSignals()...),
	}
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
