package main

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	catalognvlink "github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

var nvlinkCatalogSignalsByObservation = map[string][]string{
	gpusignals.NVLinkFabricBandwidthDegraded.ID():     {catalognvlink.SignalFabricBandwidthDegraded},
	gpusignals.NVLinkFabricRouteRecoveryActive.ID():   {catalognvlink.SignalFabricRouteRecoveryInProgress},
	gpusignals.NVLinkFabricRouteUnhealthy.ID():        {catalognvlink.SignalFabricRouteUnhealthy},
	gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(): {catalognvlink.SignalFabricAccessTimeoutRecovery},
	gpusignals.FabricManagerStatus.ID(): {
		catalognvlink.SignalFabricRegistrationIncomplete,
		catalognvlink.SignalFabricRegistrationFailed,
	},
}

// buildNVLinkMeasurements converts canonical fabric observations into the
// typed input expected by the NVLink checks. Unrelated observations are ignored.
func buildNVLinkMeasurements(observations []*observation.Observation) []health.Measurement {
	byGPU := make(map[string]*catalognvlink.NVLinkMeasurement)
	seenByGPU := make(map[string]map[string]struct{})

	for _, current := range observations {
		catalogSignals, supported := nvlinkCatalogSignalsByObservation[current.GetSignalId()]
		if !supported {
			continue
		}

		gpuID, entityErr := observationGPUID(current)
		measurement := byGPU[gpuID]
		if measurement == nil {
			measurement = &catalognvlink.NVLinkMeasurement{GPUID: gpuID}
			byGPU[gpuID] = measurement
			seenByGPU[gpuID] = make(map[string]struct{}, len(requiredNVLinkSignals))
		}

		if _, duplicate := seenByGPU[gpuID][current.GetSignalId()]; duplicate {
			measurement.AddError(
				fmt.Errorf("signal %q was observed more than once for GPU %q", current.GetSignalId(), gpuID),
				catalogSignals...,
			)
			continue
		}
		seenByGPU[gpuID][current.GetSignalId()] = struct{}{}

		if entityErr != nil {
			measurement.AddError(
				entityErr,
				append([]string{catalognvlink.SignalGPUID}, catalogSignals...)...,
			)
			continue
		}
		if failure := current.GetCollectionError(); failure != nil {
			measurement.AddError(collectionFailure(failure), catalogSignals...)
			continue
		}
		if err := applyNVLinkValue(measurement, current); err != nil {
			measurement.AddError(err, catalogSignals...)
		}
	}

	if len(byGPU) == 0 {
		return []health.Measurement{catalognvlink.ErroredNVLink(
			"gpu",
			errors.New("shared collector returned no NVLink fabric observations"),
			catalognvlink.SignalGPUID,
			catalognvlink.SignalFabricBandwidthDegraded,
			catalognvlink.SignalFabricRouteRecoveryInProgress,
			catalognvlink.SignalFabricRouteUnhealthy,
			catalognvlink.SignalFabricAccessTimeoutRecovery,
			catalognvlink.SignalFabricRegistrationIncomplete,
			catalognvlink.SignalFabricRegistrationFailed,
		)}
	}

	ids := make([]string, 0, len(byGPU))
	for gpuID := range byGPU {
		ids = append(ids, gpuID)
	}
	slices.Sort(ids)

	measurements := make([]health.Measurement, 0, len(ids))
	for _, gpuID := range ids {
		measurement := byGPU[gpuID]
		for _, signalID := range requiredNVLinkSignals {
			if _, observed := seenByGPU[gpuID][signalID]; observed {
				continue
			}
			measurement.AddError(
				fmt.Errorf("shared collector returned no observation for signal %q", signalID),
				nvlinkCatalogSignalsByObservation[signalID]...,
			)
		}
		measurements = append(measurements, *measurement)
	}
	return measurements
}

func applyNVLinkValue(measurement *catalognvlink.NVLinkMeasurement, current *observation.Observation) error {
	if current.GetSignalId() == gpusignals.FabricManagerStatus.ID() {
		return applyFabricManagerStatus(measurement, current)
	}

	value, err := boolValue(current)
	if err != nil {
		return err
	}
	switch current.GetSignalId() {
	case gpusignals.NVLinkFabricBandwidthDegraded.ID():
		measurement.FabricBandwidthDegraded = value
	case gpusignals.NVLinkFabricRouteRecoveryActive.ID():
		measurement.FabricRouteRecoveryInProgress = value
	case gpusignals.NVLinkFabricRouteUnhealthy.ID():
		measurement.FabricRouteUnhealthy = value
	case gpusignals.NVLinkFabricAccessTimeoutRecovery.ID():
		measurement.FabricAccessTimeoutRecovery = value
	default:
		return fmt.Errorf("NVLink adapter does not support signal %q", current.GetSignalId())
	}
	return nil
}

// applyFabricManagerStatus contains the Health Validation-specific conversion
// from DCGM's raw status to the two fields required by the NVLink fabric check.
// Source-level unsupported results arrive as collection errors and never reach
// this function.
func applyFabricManagerStatus(measurement *catalognvlink.NVLinkMeasurement, current *observation.Observation) error {
	status, err := int64Value(current)
	if err != nil {
		return err
	}
	switch status {
	case gpusignals.FabricManagerStatusNotSupported,
		gpusignals.FabricManagerStatusNotStarted,
		gpusignals.FabricManagerStatusInProgress:
		measurement.FabricRegistrationIncomplete = true
		measurement.FabricRegistrationFailed = false
	case gpusignals.FabricManagerStatusSuccess:
		measurement.FabricRegistrationIncomplete = false
		measurement.FabricRegistrationFailed = false
	case gpusignals.FabricManagerStatusFailure:
		measurement.FabricRegistrationIncomplete = false
		measurement.FabricRegistrationFailed = true
	case gpusignals.FabricManagerStatusUnrecognized:
		return fmt.Errorf("DCGM returned an unrecognized fabric manager status")
	case gpusignals.FabricManagerStatusNVMLTooOld:
		return fmt.Errorf("the installed NVML does not support fabric manager status")
	default:
		return fmt.Errorf("DCGM returned invalid fabric manager status %d", status)
	}
	return nil
}
