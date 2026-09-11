package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	catalognvlink "github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func TestBuildNVLinkMeasurements(t *testing.T) {
	measurements := buildNVLinkMeasurements([]*observation.Observation{
		valueObservation(gpusignals.NVLinkFabricBandwidthDegraded.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.NVLinkFabricRouteRecoveryActive.ID(), booleanValue(true), nil),
		valueObservation(gpusignals.NVLinkFabricRouteUnhealthy.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(gpusignals.FabricManagerStatusSuccess), stringPointer("1")),
	})

	require.Len(t, measurements, 1)
	measurement := measurements[0].(catalognvlink.NVLinkMeasurement)
	require.Equal(t, "GPU-0", measurement.GPUID)
	require.False(t, measurement.FabricBandwidthDegraded)
	require.True(t, measurement.FabricRouteRecoveryInProgress)
	require.False(t, measurement.FabricRouteUnhealthy)
	require.False(t, measurement.FabricAccessTimeoutRecovery)
	require.False(t, measurement.FabricRegistrationIncomplete)
	require.False(t, measurement.FabricRegistrationFailed)
	require.Empty(t, measurement.Errors())
}

func TestBuildNVLinkMeasurementsPreservesFieldFailure(t *testing.T) {
	failed := valueObservation(gpusignals.NVLinkFabricRouteUnhealthy.ID(), booleanValue(false), nil)
	failed.Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{
		Source:    "dcgm",
		Operation: "metrics",
		Category:  observation.CollectionErrorCategorySourceError,
		Detail:    "field unavailable",
	}}

	measurements := buildNVLinkMeasurements([]*observation.Observation{
		valueObservation(gpusignals.NVLinkFabricBandwidthDegraded.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.NVLinkFabricRouteRecoveryActive.ID(), booleanValue(false), nil),
		failed,
		valueObservation(gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(gpusignals.FabricManagerStatusSuccess), stringPointer("1")),
	})

	validationCatalog, err := newValidationCatalog("GB200")
	require.NoError(t, err)
	verdict, err := validationCatalog.EvaluateAll(measurements...)
	require.NoError(t, err)

	byID := make(map[string]health.CheckVerdict, len(verdict.CheckResults))
	for _, result := range verdict.CheckResults {
		byID[result.CheckID] = result
	}
	require.Equal(t, health.Pass, byID["nvlink_fabric"].Severity)
	require.Equal(t, health.Critical, byID["nvlink_fabric_health"].Severity)
}

func TestBuildNVLinkMeasurementsConvertsFabricManagerStatus(t *testing.T) {
	tests := []struct {
		name           string
		status         int64
		wantIncomplete bool
		wantFailed     bool
		wantError      bool
	}{
		{name: "not supported", status: gpusignals.FabricManagerStatusNotSupported, wantIncomplete: true},
		{name: "not started", status: gpusignals.FabricManagerStatusNotStarted, wantIncomplete: true},
		{name: "in progress", status: gpusignals.FabricManagerStatusInProgress, wantIncomplete: true},
		{name: "success", status: gpusignals.FabricManagerStatusSuccess},
		{name: "failure", status: gpusignals.FabricManagerStatusFailure, wantFailed: true},
		{name: "unrecognized", status: gpusignals.FabricManagerStatusUnrecognized, wantError: true},
		{name: "NVML too old", status: gpusignals.FabricManagerStatusNVMLTooOld, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observations := healthyFabricObservations()
			observations = append(observations,
				valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(test.status), stringPointer("1")),
			)
			measurements := buildNVLinkMeasurements(observations)

			require.Len(t, measurements, 1)
			measurement := measurements[0].(catalognvlink.NVLinkMeasurement)
			require.Equal(t, test.wantIncomplete, measurement.FabricRegistrationIncomplete)
			require.Equal(t, test.wantFailed, measurement.FabricRegistrationFailed)
			if test.wantError {
				require.NotEmpty(t, measurement.Errors())
			} else {
				require.Empty(t, measurement.Errors())
			}
		})
	}
}

func TestBuildNVLinkMeasurementsPreservesFabricManagerCollectionFailure(t *testing.T) {
	failed := valueObservation(gpusignals.FabricManagerStatus.ID(), intValue(0), stringPointer("1"))
	failed.Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{
		Source:      "dcgm",
		Operation:   "metrics",
		Category:    observation.CollectionErrorCategoryUnsupported,
		SourceError: "DCGM_FT_NOT_SUPPORTED",
		Detail:      "field is not supported",
	}}

	measurements := buildNVLinkMeasurements(append(healthyFabricObservations(), failed))

	require.Len(t, measurements, 1)
	measurement := measurements[0].(catalognvlink.NVLinkMeasurement)
	require.False(t, measurement.FabricRegistrationIncomplete)
	require.False(t, measurement.FabricRegistrationFailed)
	require.Len(t, measurement.Errors(), 1)
	require.ElementsMatch(t, []string{
		catalognvlink.SignalFabricRegistrationIncomplete,
		catalognvlink.SignalFabricRegistrationFailed,
	}, measurement.Errors()[0].FailedSignals())
}

func healthyFabricObservations() []*observation.Observation {
	return []*observation.Observation{
		valueObservation(gpusignals.NVLinkFabricBandwidthDegraded.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.NVLinkFabricRouteRecoveryActive.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.NVLinkFabricRouteUnhealthy.ID(), booleanValue(false), nil),
		valueObservation(gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(), booleanValue(false), nil),
	}
}
