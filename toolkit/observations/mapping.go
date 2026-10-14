// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package observations converts between the catalog's typed measurements
// and the shared collect observation contract.
//
// [FromMeasurements] gives toolkit sensor output the ObservationBatch shape,
// one observation per measurement field named <domain>.<field>.
// [ToMeasurements] rebuilds measurements from a batch for evaluation. It
// accepts those toolkit-shaped observations and observations from collect
// sensors under canonical signal IDs, including both in one batch, so
// sensors can move to collect one at a time.
package observations

import (
	"errors"
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

// canonicalMapping relates one canonical collect signal to the catalog
// fields it feeds on one measurement type.
type canonicalMapping struct {
	signal      collect.Signal
	measurement health.Measurement
	// apply writes the observed value into a pointer to the measurement.
	// When nil, the value is assigned to the single catalog field.
	apply func(measurement any, o *observation.Observation) error
	// unit, when set, is the unit the observation must carry.
	unit    string
	catalog []string
	// unsupportedIsNeutral leaves the fields at their zero value, marked
	// unsupported, when the source reports the signal unsupported, instead
	// of failing the checks that read them.
	unsupportedIsNeutral bool
}

var canonicalMappings = []canonicalMapping{
	{signal: cpusignals.PossibleCount, measurement: cpu.CPUMeasurement{}, catalog: []string{cpu.SignalPossibleCPUCount}, unit: "1"},
	{signal: cpusignals.OnlineCount, measurement: cpu.CPUMeasurement{}, catalog: []string{cpu.SignalOnlineCPUCount}, unit: "1"},
	{signal: cpusignals.NUMANodeOnlineCount, measurement: cpu.CPUMeasurement{}, catalog: []string{cpu.SignalNUMANodeCount}, unit: "1"},
	{signal: gpusignals.PowerDraw, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalAveragePowerDrawW}, unit: "watts"},
	{signal: gpusignals.PowerLimitRequested, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalPowerLimitW}, unit: "watts"},
	{signal: gpusignals.PowerLimitEnforced, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalEnforcedPowerLimitW}, unit: "watts"},
	{signal: gpusignals.PerformanceState, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalGPUPstate}},
	{
		signal: gpusignals.HardwareSlowdownActive, measurement: gpu.GPUMeasurement{},
		catalog: []string{gpu.SignalHWSlowdown}, unsupportedIsNeutral: true,
	},
	{
		signal: gpusignals.HardwareThermalSlowdownActive, measurement: gpu.GPUMeasurement{},
		catalog: []string{gpu.SignalHWThermalSlowdown}, unsupportedIsNeutral: true,
	},
	{
		signal: gpusignals.HardwarePowerBrakeSlowdownActive, measurement: gpu.GPUMeasurement{},
		catalog: []string{gpu.SignalHWPowerBrakeSlowdown}, unsupportedIsNeutral: true,
	},
	{
		signal: gpusignals.NVLinkFabricBandwidthDegraded, measurement: nvlink.NVLinkMeasurement{},
		catalog: []string{nvlink.SignalFabricBandwidthDegraded}, unsupportedIsNeutral: true,
	},
	{
		signal: gpusignals.NVLinkFabricRouteRecoveryActive, measurement: nvlink.NVLinkMeasurement{},
		catalog: []string{nvlink.SignalFabricRouteRecoveryInProgress}, unsupportedIsNeutral: true,
	},
	{
		signal: gpusignals.NVLinkFabricRouteUnhealthy, measurement: nvlink.NVLinkMeasurement{},
		catalog: []string{nvlink.SignalFabricRouteUnhealthy}, unsupportedIsNeutral: true,
	},
	{
		signal: gpusignals.NVLinkFabricAccessTimeoutRecovery, measurement: nvlink.NVLinkMeasurement{},
		catalog: []string{nvlink.SignalFabricAccessTimeoutRecovery}, unsupportedIsNeutral: true,
	},
	{
		signal:      gpusignals.FabricManagerStatus,
		measurement: nvlink.NVLinkMeasurement{},
		catalog: []string{
			nvlink.SignalFabricRegistrationIncomplete,
			nvlink.SignalFabricRegistrationFailed,
		},
		apply: applyFabricManagerStatus,
	},
}

// CanonicalSignals returns the canonical collect signal IDs [ToMeasurements]
// reads, across every domain. A single-source sensor rejects signals it does
// not collect, so restrict one with [CanonicalSignalsFor] instead.
func CanonicalSignals() []string {
	ids := make([]string, 0, len(canonicalMappings))
	for _, mapped := range canonicalMappings {
		ids = append(ids, mapped.signal.ID())
	}
	return ids
}

// CanonicalSignalsFor returns the canonical collect signal IDs that feed
// the catalog's measurement for category, in mapping order, so a domain's
// collect adapter requests what this table maps rather than its own copy.
// It is empty for a category no canonical signal feeds yet.
func CanonicalSignalsFor(category health.Category) []string {
	var ids []string
	for _, mapped := range canonicalMappings {
		if mapped.measurement.Category() == category {
			ids = append(ids, mapped.signal.ID())
		}
	}
	return ids
}

// applyFabricManagerStatus maps the raw Fabric Manager status onto the two
// registration signals the NVLink fabric check reads. A source-level
// unsupported result arrives as a NoValue and never reaches here.
func applyFabricManagerStatus(measurement any, current *observation.Observation) error {
	m := measurement.(*nvlink.NVLinkMeasurement)
	integer, ok := current.GetValue().GetKind().(*observation.Value_IntValue)
	if !ok {
		return fmt.Errorf("signal %q does not have an integer value", current.GetSignalId())
	}
	switch status := integer.IntValue; status {
	case gpusignals.FabricManagerStatusNotSupported,
		gpusignals.FabricManagerStatusNotStarted,
		gpusignals.FabricManagerStatusInProgress:
		m.FabricRegistrationIncomplete, m.FabricRegistrationFailed = true, false
	case gpusignals.FabricManagerStatusSuccess:
		m.FabricRegistrationIncomplete, m.FabricRegistrationFailed = false, false
	case gpusignals.FabricManagerStatusFailure:
		m.FabricRegistrationIncomplete, m.FabricRegistrationFailed = false, true
	case gpusignals.FabricManagerStatusUnrecognized:
		return errors.New("unrecognized fabric manager status")
	case gpusignals.FabricManagerStatusNVMLTooOld:
		return errors.New("the installed NVML does not support fabric manager status")
	default:
		return fmt.Errorf("invalid fabric manager status %d", status)
	}
	return nil
}
