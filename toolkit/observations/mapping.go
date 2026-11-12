// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package observations converts between the catalog's typed measurements
// and the shared collect observation contract.
//
// [FromMeasurements] writes measurements as observations, one per measurement
// field named <domain>.<field>; this is the format the health-validation
// collect command emits. [ToMeasurements] rebuilds measurements from a batch
// for evaluation. It reads that measurement-shaped format and observations
// from collect sensors under mapped signal IDs, including both in one batch.
// A mapped signal is a collect signal with a row in this package's mapping
// table.
package observations

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	cpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/cpu"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// mapping relates one collect signal to the catalog
// fields it feeds on one measurement type.
type mapping struct {
	signal      collect.Signal
	measurement health.Measurement
	// apply writes the observed value into a pointer to the measurement.
	// When nil, the value is assigned to the single catalog field.
	apply func(measurement any, o *observation.Observation) error
	// unsupportedValue, when set, is the value a reported-unsupported NoValue
	// stands for.
	unsupportedValue *observation.Value
	// unit, when set, is the unit the observation must carry.
	unit    string
	catalog []string
	// unsupportedIsNeutral leaves the fields at their zero value, marked
	// unsupported, when the source reports the signal unsupported, instead
	// of failing the checks that read them.
	unsupportedIsNeutral bool
	// aggregated rows feed NVLink fields that ToMeasurements computes per GPU
	// from several observations (see nvlinkLinks): per-link and per-pair
	// facts, and the link count and common speed the speed fallback reads.
	aggregated bool
}

var mappings = []mapping{
	{signal: cpusignals.PossibleCount, measurement: cpu.CPUMeasurement{}, catalog: []string{cpu.SignalPossibleCPUCount}, unit: "1"},
	{signal: cpusignals.OnlineCount, measurement: cpu.CPUMeasurement{}, catalog: []string{cpu.SignalOnlineCPUCount}, unit: "1"},
	{signal: cpusignals.NUMANodeOnlineCount, measurement: cpu.CPUMeasurement{}, catalog: []string{cpu.SignalNUMANodeCount}, unit: "1"},
	{signal: gpuinventory.GPUCount, measurement: node.NodeMeasurement{}, catalog: []string{node.SignalGPUCount}, unit: "1"},
	// An average only on some architectures (instantaneous on GA100 and
	// older); the CLI's GPU adapter turns other readings into NOT_SUPPORTED.
	{signal: gpusignals.PowerDraw, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalAveragePowerDrawW}, unit: "watts"},
	{signal: gpusignals.PowerLimitRequested, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalPowerLimitW}, unit: "watts"},
	{signal: gpusignals.PowerLimitEnforced, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalEnforcedPowerLimitW}, unit: "watts"},
	{signal: gpusignals.PerformanceState, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalGPUPstate}, apply: applyPerformanceState},
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
	{signal: gpusignals.ProcessCount, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalComputeProcessCount}, unit: "1"},
	// Folded per GPU by gpuProcesses.
	{signal: gpusignals.ProcessCmdline, measurement: gpu.GPUMeasurement{}, catalog: []string{gpu.SignalComputeProcessCmdlines}},
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
		apply:            applyFabricManagerStatus,
		unsupportedValue: &observation.Value{Kind: &observation.Value_IntValue{IntValue: gpusignals.FabricManagerStatusNotSupported}},
	},
	{signal: gpusignals.NVLinkState, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalInactiveCount}, aggregated: true},
	{signal: gpusignals.NVLinkLinkCount, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalLinkCount}, aggregated: true},
	{signal: gpusignals.NVLinkSpeedCommon, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalSpeedMBytesPerSec}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointSpeed, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalSpeedMBytesPerSec}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointCapabilityP2PSupported, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalAllLinksSupportP2P}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointCapabilitySysmemAccess, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalAllLinksSupportSysmemAccess}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointCapabilityP2PAtomics, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalAllLinksSupportP2PAtomics}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointCapabilitySysmemAtomics, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalAllLinksSupportSysmemAtomics}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointCapabilitySLIBridge, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalAllLinksSupportSLI}, aggregated: true},
	{signal: gpusignals.NVLinkEndpointCapabilityValid, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalAllLinksSupportLink}, aggregated: true},
	{signal: gpusignals.NVLinkP2PStatus, measurement: nvlink.NVLinkMeasurement{}, catalog: []string{nvlink.SignalUnhealthyP2PPeerCount}, aggregated: true},
}

// canonicalFamilies are collect signal families that build one measurement
// together rather than field by field: one measurement aggregates repeated
// journal-entry events, so every signal of a family feeds every catalog
// signal of its measurement.
var canonicalFamilies = []struct {
	signals  func() collect.Signals
	catalog  func() []string
	category health.Category
}{
	{signals: xidsignals.All, catalog: xid.MeasurementSignals, category: health.Xid},
	{signals: ibsignals.LinkFlapSignals, catalog: infiniband.MeasurementSignals, category: health.InfiniBand},
}

// MappedSignals returns the mapped collect signal IDs [ToMeasurements]
// reads, across every domain. A single-source sensor rejects signals it does
// not collect, so restrict one to the signals it produces; a domain such as
// NVLink maps signals from more than one source.
func MappedSignals() []string {
	ids := make([]string, 0, len(mappings))
	for _, mapped := range mappings {
		ids = append(ids, mapped.signal.ID())
	}
	for _, family := range canonicalFamilies {
		ids = append(ids, family.signals().IDs()...)
	}
	return ids
}

// MappedSignalsFor returns the mapped collect signal IDs that feed
// the catalog's measurements for category, mapping-table rows first, so a
// domain's collect adapter requests what this package maps rather than its
// own copy. It is empty for a category no mapped signal feeds yet.
func MappedSignalsFor(category health.Category) []string {
	var ids []string
	for _, mapped := range mappingsFor(category) {
		ids = append(ids, mapped.signal.ID())
	}
	for _, family := range canonicalFamilies {
		if family.category == category {
			ids = append(ids, family.signals().IDs()...)
		}
	}
	return ids
}

// MappedFeeds maps each mapped collect signal ID that feeds the
// catalog's measurements for category to the catalog signals it fills.
func MappedFeeds(category health.Category) map[string][]string {
	feeds := make(map[string][]string)
	for _, mapped := range mappingsFor(category) {
		feeds[mapped.signal.ID()] = slices.Clone(mapped.catalog)
	}
	for _, family := range canonicalFamilies {
		if family.category != category {
			continue
		}
		for _, id := range family.signals().IDs() {
			feeds[id] = family.catalog()
		}
	}
	return feeds
}

// mappingsFor returns the rows feeding category's measurement, in table order.
func mappingsFor(category health.Category) []mapping {
	return slices.DeleteFunc(slices.Clone(mappings), func(m mapping) bool {
		return m.measurement.Category() != category
	})
}

// maxPerformanceState is P15, the lowest NVML performance state. NVML and
// DCGM report an undeterminable state as 32 (NVML_PSTATE_UNKNOWN).
const maxPerformanceState = 15

// applyPerformanceState fails a pstate outside P0-P15 rather than letting the
// unknown sentinel fail the threshold.
func applyPerformanceState(measurement any, current *observation.Observation) error {
	var state int64
	if err := setField(reflect.ValueOf(&state).Elem(), current); err != nil {
		return err
	}
	if state < 0 || state > maxPerformanceState {
		return fmt.Errorf("performance state unknown (%d)", state)
	}
	measurement.(*gpu.GPUMeasurement).GPUPstate = state
	return nil
}

// applyFabricManagerStatus maps the raw Fabric Manager status onto the two
// registration signals the NVLink fabric check reads. A status reported
// unsupported reads as FabricManagerStatusNotSupported: fabric registration
// is mandatory where the fabric checks run, so a GPU that cannot report it is
// unregistered.
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
