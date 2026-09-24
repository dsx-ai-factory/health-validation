package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	cataloggpu "github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func TestBuildGPUMeasurements(t *testing.T) {
	measurements := buildGPUMeasurements([]*observation.Observation{
		valueObservation(gpusignals.PowerDraw.ID(), doubleValue(75), stringPointer("watts")),
		valueObservation(gpusignals.PowerLimitRequested.ID(), doubleValue(700), stringPointer("watts")),
		valueObservation(gpusignals.PowerLimitEnforced.ID(), doubleValue(700), stringPointer("watts")),
		valueObservation(gpusignals.PerformanceState.ID(), intValue(0), nil),
		valueObservation(gpusignals.HardwareSlowdownActive.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.HardwareThermalSlowdownActive.ID(), booleanValue(true), nil),
		valueObservation(gpusignals.HardwarePowerBrakeSlowdownActive.ID(), booleanValue(false), nil),
	})

	require.Len(t, measurements, 1)
	measurement := measurements[0].(cataloggpu.GPUMeasurement)
	require.Equal(t, "GPU-0", measurement.GPUID)
	require.Equal(t, float64(75), measurement.AveragePowerDrawW)
	require.Equal(t, float64(700), measurement.PowerLimitW)
	require.Equal(t, float64(700), measurement.EnforcedPowerLimitW)
	require.Zero(t, measurement.GPUPstate)
	require.False(t, measurement.HWSlowdown)
	require.True(t, measurement.HWThermalSlowdown)
	require.False(t, measurement.HWPowerBrakeSlowdown)
	require.Empty(t, measurement.Errors())
}

func TestBuildGPUMeasurementsPreservesPerSignalFailure(t *testing.T) {
	failed := &observation.Observation{
		SignalId:   gpusignals.PerformanceState.ID(),
		Source:     "dcgm",
		Target:     observation.GPUTarget("GPU-0"),
		ObservedAt: timestamppb.Now(),
		Outcome: &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{
			Source:    "dcgm",
			Operation: "metrics",
			Category:  observation.CollectionErrorCategoryTimeout,
			Detail:    "read timed out",
		}},
	}
	measurements := buildGPUMeasurements([]*observation.Observation{
		valueObservation(gpusignals.PowerDraw.ID(), doubleValue(75), stringPointer("watts")),
		valueObservation(gpusignals.PowerLimitRequested.ID(), doubleValue(700), stringPointer("watts")),
		valueObservation(gpusignals.PowerLimitEnforced.ID(), doubleValue(700), stringPointer("watts")),
		valueObservation(gpusignals.HardwareSlowdownActive.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.HardwareThermalSlowdownActive.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.HardwarePowerBrakeSlowdownActive.ID(), booleanValue(false), nil),
		failed,
	})

	validationCatalog, err := newValidationCatalog("H100")
	require.NoError(t, err)
	verdict, err := validationCatalog.EvaluateAll(measurements...)
	require.NoError(t, err)
	require.Len(t, verdict.CheckResults, 3)

	byID := make(map[string]health.CheckVerdict, len(verdict.CheckResults))
	for _, result := range verdict.CheckResults {
		byID[result.CheckID] = result
	}
	require.Equal(t, health.Pass, byID["gpu_power_limit"].Severity)
	require.Equal(t, health.Critical, byID["gpu_power_state"].Severity)
	require.Equal(t, []health.RemediationAction{health.RemediationActionRetryCollection}, byID["gpu_power_state"].RecommendedAction.RemediationActions)

	measurement := measurements[0].(cataloggpu.GPUMeasurement)
	require.Len(t, measurement.Errors(), 1)
	require.True(t, errors.Is(measurement.Errors()[0].Err(), health.ErrCollectionTimeout))
}

func TestBuildGPUMeasurementsPreservesNoTargets(t *testing.T) {
	noTargets := observation.NewNoValueObservation(
		gpusignals.PerformanceState.ID(),
		nil,
		"dcgm",
		nil,
		timestamppb.Now(),
		&observation.NoValue{
			Reason: observation.NoValueReasonNoTargets,
			Detail: "No GPUs detected",
		},
	)

	measurements := buildGPUMeasurements([]*observation.Observation{noTargets})

	require.Len(t, measurements, 1)
	measurement := measurements[0].(cataloggpu.GPUMeasurement)
	require.Equal(t, "gpu", measurement.GPUID)
	require.NotEmpty(t, measurement.Errors())
	require.Contains(t, measurement.Errors()[0].Err().Error(), "NO_VALUE_REASON_NO_TARGETS")
}

func valueObservation(signalID string, value *observation.Value, unit *string) *observation.Observation {
	return &observation.Observation{
		SignalId:   signalID,
		Source:     "dcgm",
		Target:     observation.GPUTarget("GPU-0"),
		ObservedAt: timestamppb.Now(),
		Unit:       unit,
		Outcome:    &observation.Observation_Value{Value: value},
	}
}

func intValue(value int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: value}}
}

func doubleValue(value float64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: value}}
}

func booleanValue(value bool) *observation.Value {
	return &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: value}}
}

func stringPointer(value string) *string { return &value }
