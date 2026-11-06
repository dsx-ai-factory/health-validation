// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"fmt"
	"maps"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

// sourceLevelGPU identifies failures not attributable to one GPU.
const sourceLevelGPU = "gpu"

// domainAdapter assembles one catalog category from mapped signals.
type domainAdapter struct {
	errored func(error) []health.Measurement
	// complete, when set, adds a failed measurement for each type Build
	// must always produce but the observations did not build.
	complete func([]health.Measurement) []health.Measurement
	// feeds maps each collect signal ID to the catalog signals it fills;
	// several when one raw value is split into multiple catalog fields.
	feeds    map[string][]string
	category health.Category
}

// CPU replaces toolkit.CPUSensor for the stock CLI's CPU domain.
func CPU() *domainAdapter {
	return newDomainAdapter(health.CPU, erroredCPU)
}

// Node replaces toolkit.NodeSensor for the stock CLI's node domain. Its
// gpu_count comes from the shared GPU inventory (gpuinventory.GPUCount).
func Node() *domainAdapter {
	return newDomainAdapter(health.Node, erroredNode)
}

// GPU replaces toolkit.GPUSensor for the stock CLI's GPU domain.
func GPU() *domainAdapter {
	return newDomainAdapter(health.GPU, erroredGPU)
}

// NVLink replaces toolkit.NVLinkSensor for the stock CLI's NVLink domain.
func NVLink() *domainAdapter {
	return newDomainAdapter(health.NVLink, erroredNVLink)
}

// newDomainAdapter requests every signal the converter maps onto category,
// so the adapter and the converter cannot disagree.
func newDomainAdapter(category health.Category, errored func(error) []health.Measurement) *domainAdapter {
	return &domainAdapter{
		category: category, errored: errored,
		feeds: observations.MappedFeeds(category),
	}
}

// Name is the catalog category, which app reserves for the built-in adapter.
func (a *domainAdapter) Name() string { return a.category.String() }

func (a *domainAdapter) Category() health.Category { return a.category }

func (a *domainAdapter) Signals() map[string][]string { return maps.Clone(a.feeds) }

// Build consumes observations already validated by the bridge.
func (a *domainAdapter) Build(obs []*observation.Observation) []health.Measurement {
	measurements := adaptCategory(a.category, a.feeds, obs)
	if a.complete != nil {
		measurements = a.complete(measurements)
	}
	if len(measurements) == 0 {
		return a.Errored(fmt.Errorf("no %s observations were collected", a.Name()))
	}
	return measurements
}

func (a *domainAdapter) Errored(err error) []health.Measurement { return a.errored(err) }

func erroredCPU(err error) []health.Measurement {
	return []health.Measurement{cpu.ErroredCPU(err)}
}

// adapterSource names a built-in adapter as the source of a whole-domain
// failure, distinct from a toolkit sensor failing the same domain.
func adapterSource(category health.Category) string { return "collect." + category.String() }

func erroredNode(err error) []health.Measurement {
	return []health.Measurement{node.ErroredNode(err, node.MeasurementSignals()...)}
}

func erroredGPU(err error) []health.Measurement {
	m := gpu.ErroredGPU(sourceLevelGPU, err, gpu.MeasurementSignals()...)
	m.SetEntityKey(health.SourceEntity(adapterSource(health.GPU)))
	return []health.Measurement{m}
}

func erroredNVLink(err error) []health.Measurement {
	// The legacy NVLink sensor names a whole-domain failure after its domain.
	m := nvlink.ErroredNVLink(health.NVLink.String(), err, nvlink.MeasurementSignals()...)
	m.SetEntityKey(health.SourceEntity(adapterSource(health.NVLink)))
	return []health.Measurement{m}
}

// adaptCategory converts the observations of signals feeds maps, together, so
// a source-wide failure meets the GPUs other sources read, and keeps
// measurements of category. The converter keys each GPU by UUID, or by source
// and slot when a source could not read its UUID.
func adaptCategory(category health.Category, feeds map[string][]string, obs []*observation.Observation) []health.Measurement {
	obs = slices.DeleteFunc(slices.Clone(obs), func(o *observation.Observation) bool {
		_, ok := feeds[o.GetSignalId()]
		return !ok
	})
	return slices.DeleteFunc(observations.ToMeasurements(obs), func(m health.Measurement) bool { return m.Category() != category })
}
