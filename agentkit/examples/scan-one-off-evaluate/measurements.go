package main

import (
	"errors"
	"fmt"
	"slices"

	cataloggpu "github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

var gpuCatalogSignalByObservation = map[string]string{
	gpusignals.PowerDraw.ID():                        cataloggpu.SignalAveragePowerDrawW,
	gpusignals.PowerLimitRequested.ID():              cataloggpu.SignalPowerLimitW,
	gpusignals.PowerLimitEnforced.ID():               cataloggpu.SignalEnforcedPowerLimitW,
	gpusignals.PerformanceState.ID():                 cataloggpu.SignalGPUPstate,
	gpusignals.HardwareSlowdownActive.ID():           cataloggpu.SignalHWSlowdown,
	gpusignals.HardwareThermalSlowdownActive.ID():    cataloggpu.SignalHWThermalSlowdown,
	gpusignals.HardwarePowerBrakeSlowdownActive.ID(): cataloggpu.SignalHWPowerBrakeSlowdown,
}

func buildMeasurements(observations []*observation.Observation) []health.Measurement {
	measurements := buildGPUMeasurements(observations)
	return append(measurements, buildNVLinkMeasurements(observations)...)
}

// buildGPUMeasurements is the adapter between the shared collection contract
// and Health Validation's typed GPU input. Collection and conversion failures
// are attached to the affected signal, allowing unrelated checks to continue.
func buildGPUMeasurements(observations []*observation.Observation) []health.Measurement {
	byGPU := make(map[string]*cataloggpu.GPUMeasurement)
	seenByGPU := make(map[string]map[string]struct{})

	for _, current := range observations {
		catalogSignal, supported := gpuCatalogSignalByObservation[current.GetSignalId()]
		if !supported {
			continue
		}

		gpuID, targetErr := observationGPUID(current)
		measurement := byGPU[gpuID]
		if measurement == nil {
			measurement = &cataloggpu.GPUMeasurement{GPUID: gpuID}
			byGPU[gpuID] = measurement
			seenByGPU[gpuID] = make(map[string]struct{}, len(requiredGPUSignals))
		}

		if _, duplicate := seenByGPU[gpuID][current.GetSignalId()]; duplicate {
			measurement.AddError(
				fmt.Errorf("signal %q was observed more than once for GPU %q", current.GetSignalId(), gpuID),
				catalogSignal,
			)
			continue
		}
		seenByGPU[gpuID][current.GetSignalId()] = struct{}{}

		if targetErr != nil {
			measurement.AddError(targetErr, cataloggpu.SignalGPUID, catalogSignal)
			continue
		}
		if failure := current.GetCollectionError(); failure != nil {
			measurement.AddError(collectionFailure(failure), catalogSignal)
			continue
		}
		if noValue := current.GetNoValue(); noValue != nil {
			measurement.AddError(noValueFailure(current.GetSource(), noValue), catalogSignal)
			continue
		}
		if err := applyGPUValue(measurement, current); err != nil {
			measurement.AddError(err, catalogSignal)
		}
	}

	if len(byGPU) == 0 {
		return []health.Measurement{cataloggpu.ErroredGPU(
			"gpu",
			errors.New("shared collector returned no GPU observations"),
			cataloggpu.MeasurementSignals...,
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
		for _, signalID := range requiredGPUSignals {
			if _, observed := seenByGPU[gpuID][signalID]; observed {
				continue
			}
			measurement.AddError(
				fmt.Errorf("shared collector returned no observation for signal %q", signalID),
				gpuCatalogSignalByObservation[signalID],
			)
		}
		measurements = append(measurements, *measurement)
	}
	return measurements
}

func observationGPUID(current *observation.Observation) (string, error) {
	target := current.GetTarget()
	if target == nil {
		// Source-level failures have no GPU identity. Group them into one
		// domain measurement so the affected checks still receive the errors.
		if current.GetCollectionError() != nil || current.GetNoValue() != nil {
			return "gpu", nil
		}
		return "gpu", fmt.Errorf("signal %q has no GPU target", current.GetSignalId())
	}
	var gpuID string
	switch {
	case target.GetGpu() != nil:
		gpuID = target.GetGpu().GetId()
	case target.GetNvLinkEndpoint() != nil:
		gpuID = target.GetNvLinkEndpoint().GetGpu().GetId()
	case target.GetGpuPair() != nil:
		gpuID = target.GetGpuPair().GetSourceGpu().GetId()
	default:
		return "gpu", fmt.Errorf("signal %q does not identify a GPU target", current.GetSignalId())
	}
	if gpuID == "" {
		return "gpu", fmt.Errorf("signal %q has no GPU identifier", current.GetSignalId())
	}
	return gpuID, nil
}

func applyGPUValue(measurement *cataloggpu.GPUMeasurement, current *observation.Observation) error {
	switch {
	case current.GetSignalId() == gpusignals.PowerDraw.ID():
		value, err := wattsValue(current)
		if err != nil {
			return err
		}
		measurement.AveragePowerDrawW = value
	case current.GetSignalId() == gpusignals.PowerLimitRequested.ID():
		value, err := wattsValue(current)
		if err != nil {
			return err
		}
		measurement.PowerLimitW = value
	case current.GetSignalId() == gpusignals.PowerLimitEnforced.ID():
		value, err := wattsValue(current)
		if err != nil {
			return err
		}
		measurement.EnforcedPowerLimitW = value
	case current.GetSignalId() == gpusignals.PerformanceState.ID():
		value, err := int64Value(current)
		if err != nil {
			return err
		}
		measurement.GPUPstate = value
	case current.GetSignalId() == gpusignals.HardwareSlowdownActive.ID():
		value, err := boolValue(current)
		if err != nil {
			return err
		}
		measurement.HWSlowdown = value
	case current.GetSignalId() == gpusignals.HardwareThermalSlowdownActive.ID():
		value, err := boolValue(current)
		if err != nil {
			return err
		}
		measurement.HWThermalSlowdown = value
	case current.GetSignalId() == gpusignals.HardwarePowerBrakeSlowdownActive.ID():
		value, err := boolValue(current)
		if err != nil {
			return err
		}
		measurement.HWPowerBrakeSlowdown = value
	default:
		return fmt.Errorf("GPU adapter does not support signal %q", current.GetSignalId())
	}
	return nil
}

func wattsValue(current *observation.Observation) (float64, error) {
	if current.Unit == nil || current.GetUnit() != "watts" {
		return 0, fmt.Errorf("signal %q has unit %q, expected %q", current.GetSignalId(), current.GetUnit(), "watts")
	}
	return numericValue(current)
}

func boolValue(current *observation.Observation) (bool, error) {
	value := current.GetValue()
	if value == nil {
		return false, fmt.Errorf("signal %q has no value", current.GetSignalId())
	}
	boolean, ok := value.Kind.(*observation.Value_BoolValue)
	if !ok {
		return false, fmt.Errorf("signal %q does not have a boolean value", current.GetSignalId())
	}
	return boolean.BoolValue, nil
}

func numericValue(current *observation.Observation) (float64, error) {
	value := current.GetValue()
	if value == nil {
		return 0, fmt.Errorf("signal %q has no value", current.GetSignalId())
	}
	switch kind := value.Kind.(type) {
	case *observation.Value_IntValue:
		return float64(kind.IntValue), nil
	case *observation.Value_DoubleValue:
		return kind.DoubleValue, nil
	default:
		return 0, fmt.Errorf("signal %q has a non-numeric value", current.GetSignalId())
	}
}

func int64Value(current *observation.Observation) (int64, error) {
	value := current.GetValue()
	if value == nil {
		return 0, fmt.Errorf("signal %q has no value", current.GetSignalId())
	}
	integer, ok := value.Kind.(*observation.Value_IntValue)
	if !ok {
		return 0, fmt.Errorf("signal %q does not have an integer value", current.GetSignalId())
	}
	return integer.IntValue, nil
}

func collectionFailure(failure *observation.CollectionError) error {
	detail := fmt.Sprintf(
		"%s operation %q failed with %s",
		failure.GetSource(), failure.GetOperation(), failure.GetCategory(),
	)
	if failure.GetSourceError() != "" {
		detail += fmt.Sprintf(" (%s)", failure.GetSourceError())
	}
	if failure.GetDetail() != "" {
		detail += ": " + failure.GetDetail()
	}
	if failure.GetCategory() == observation.CollectionErrorCategoryTimeout {
		return fmt.Errorf("%w: %s", health.ErrCollectionTimeout, detail)
	}
	return errors.New(detail)
}

func noValueFailure(source string, noValue *observation.NoValue) error {
	detail := fmt.Sprintf("%s returned no value (%s)", source, noValue.GetReason())
	if noValue.GetSourceStatus() != "" {
		detail += fmt.Sprintf(" (%s)", noValue.GetSourceStatus())
	}
	if noValue.GetDetail() != "" {
		detail += ": " + noValue.GetDetail()
	}
	return errors.New(detail)
}
