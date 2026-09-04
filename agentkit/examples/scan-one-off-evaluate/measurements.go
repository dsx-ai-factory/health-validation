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

var catalogSignalByObservation = map[string]string{
	gpusignals.PowerLimitRequested.ID(): cataloggpu.SignalPowerLimitW,
	gpusignals.PowerLimitEnforced.ID():  cataloggpu.SignalEnforcedPowerLimitW,
	gpusignals.PerformanceState.ID():    cataloggpu.SignalGPUPstate,
}

// buildGPUMeasurements is the adapter between the shared collection contract
// and Health Validation's typed GPU input. Collection and conversion failures
// are attached to the affected signal, allowing unrelated checks to continue.
func buildGPUMeasurements(observations []*observation.Observation) []health.Measurement {
	byGPU := make(map[string]*cataloggpu.GPUMeasurement)
	seenByGPU := make(map[string]map[string]struct{})

	for _, current := range observations {
		catalogSignal, supported := catalogSignalByObservation[current.GetSignalId()]
		if !supported {
			continue
		}

		gpuID, entityErr := observationGPUID(current)
		measurement := byGPU[gpuID]
		if measurement == nil {
			measurement = &cataloggpu.GPUMeasurement{GPUID: gpuID}
			byGPU[gpuID] = measurement
			seenByGPU[gpuID] = make(map[string]struct{}, len(requiredSignals))
		}

		if _, duplicate := seenByGPU[gpuID][current.GetSignalId()]; duplicate {
			measurement.AddError(
				fmt.Errorf("signal %q was observed more than once for GPU %q", current.GetSignalId(), gpuID),
				catalogSignal,
			)
			continue
		}
		seenByGPU[gpuID][current.GetSignalId()] = struct{}{}

		if entityErr != nil {
			measurement.AddError(entityErr, cataloggpu.SignalGPUID, catalogSignal)
			continue
		}
		if failure := current.GetCollectionError(); failure != nil {
			measurement.AddError(collectionFailure(failure), catalogSignal)
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
		for _, signalID := range requiredSignals {
			if _, observed := seenByGPU[gpuID][signalID]; observed {
				continue
			}
			measurement.AddError(
				fmt.Errorf("shared collector returned no observation for signal %q", signalID),
				catalogSignalByObservation[signalID],
			)
		}
		measurements = append(measurements, *measurement)
	}
	return measurements
}

func observationGPUID(current *observation.Observation) (string, error) {
	entity := current.GetEntity()
	if entity == nil {
		// Source-level failures have no GPU identity. Group them into one
		// domain measurement so the affected checks still receive the errors.
		if current.GetCollectionError() != nil {
			return "gpu", nil
		}
		return "gpu", fmt.Errorf("signal %q has no GPU entity", current.GetSignalId())
	}
	if entity.GetType() != "gpu" {
		return entity.GetId(), fmt.Errorf(
			"signal %q has entity type %q, expected %q",
			current.GetSignalId(), entity.GetType(), "gpu",
		)
	}
	if entity.GetId() == "" {
		return "gpu", fmt.Errorf("signal %q has no GPU identifier", current.GetSignalId())
	}
	return entity.GetId(), nil
}

func applyGPUValue(measurement *cataloggpu.GPUMeasurement, current *observation.Observation) error {
	switch {
	case current.GetSignalId() == gpusignals.PowerLimitRequested.ID():
		value, err := powerLimitWatts(current)
		if err != nil {
			return err
		}
		measurement.PowerLimitW = value
	case current.GetSignalId() == gpusignals.PowerLimitEnforced.ID():
		value, err := powerLimitWatts(current)
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
	default:
		return fmt.Errorf("GPU adapter does not support signal %q", current.GetSignalId())
	}
	return nil
}

func powerLimitWatts(current *observation.Observation) (float64, error) {
	if current.Unit == nil || current.GetUnit() != "watts" {
		return 0, fmt.Errorf("signal %q has unit %q, expected %q", current.GetSignalId(), current.GetUnit(), "watts")
	}
	return numericValue(current)
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
