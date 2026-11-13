// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"maps"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// linkFlapSinceUnknown labels a link-flap failure whose window is unknown,
// as observations does.
const linkFlapSinceUnknown = "boot"

// InfiniBand is the link-flap part of the category's built-in adapter
// ([InfiniBandDomain]). It replaces toolkit.RDMALinkFlapSensor for the stock
// CLI's link-flap measurement.
func InfiniBand() *domainAdapter {
	a := newDomainAdapter(health.InfiniBand, erroredInfiniBand)
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

// adapterPart is one measurement type of a built-in adapter that joins
// several, each assembled from its own signals.
type adapterPart interface {
	Signals() map[string][]string
	Build([]*observation.Observation) []health.Measurement
	Errored(error) []health.Measurement
}

// infiniBandDomain is the InfiniBand category's one built-in adapter. It joins
// the link-flap measurement ([InfiniBand]) and the RDMA fabric port
// measurement ([RDMAPorts]), which come from different sensors.
type infiniBandDomain struct {
	parts []adapterPart
}

// InfiniBandDomain returns the category's built-in adapter. linkFlap and ports
// select its parts; app leaves a part out when a caller backend supplies that
// measurement instead.
func InfiniBandDomain(linkFlap, ports bool) *infiniBandDomain {
	d := &infiniBandDomain{}
	if linkFlap {
		d.parts = append(d.parts, InfiniBand())
	}
	if ports {
		d.parts = append(d.parts, RDMAPorts())
	}
	return d
}

// Name is the catalog category, which app reserves for the built-in adapter.
func (*infiniBandDomain) Name() string { return health.InfiniBand.String() }

func (*infiniBandDomain) Category() health.Category { return health.InfiniBand }

func (d *infiniBandDomain) Signals() map[string][]string {
	feeds := make(map[string][]string)
	for _, p := range d.parts {
		maps.Copy(feeds, p.Signals())
	}
	return feeds
}

// Build assembles every part. Each part reads only its own signals.
func (d *infiniBandDomain) Build(obs []*observation.Observation) []health.Measurement {
	return buildParts(d.parts, obs)
}

func (d *infiniBandDomain) Errored(err error) []health.Measurement {
	return erroredParts(d.parts, err)
}

// Select narrows Build and Errored to the parts that read a requested signal.
// A part no runnable check needs is then neither built nor failed, while a
// requested part that collected nothing still fails, as a separate adapter
// would.
func (d *infiniBandDomain) Select(requested []string) (build func([]*observation.Observation) []health.Measurement, errored func(error) []health.Measurement) {
	var parts []adapterPart
	for _, p := range d.parts {
		for id := range p.Signals() {
			if slices.Contains(requested, id) {
				parts = append(parts, p)
				break
			}
		}
	}
	return func(obs []*observation.Observation) []health.Measurement { return buildParts(parts, obs) },
		func(err error) []health.Measurement { return erroredParts(parts, err) }
}

func buildParts(parts []adapterPart, obs []*observation.Observation) []health.Measurement {
	var measurements []health.Measurement
	for _, p := range parts {
		measurements = append(measurements, p.Build(obs)...)
	}
	return measurements
}

func erroredParts(parts []adapterPart, err error) []health.Measurement {
	var measurements []health.Measurement
	for _, p := range parts {
		measurements = append(measurements, p.Errored(err)...)
	}
	return measurements
}
