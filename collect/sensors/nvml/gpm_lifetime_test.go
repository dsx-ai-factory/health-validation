package nvml

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

func TestGPMSecondAllocationFailureFreesFirst(t *testing.T) {
	public, err := NewGPM(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": &profilingDevice{supported: 1}}}, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	sensor := public.(*gpmSensor)
	first := &profilingSample{}
	calls := 0
	sensor.alloc = func() (nvml.GpmSample, nvml.Return) {
		calls++
		if calls == 1 {
			return first, nvml.SUCCESS
		}
		return nil, nvml.ERROR_NO_PERMISSION
	}
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Equal(t, 1, first.freed)
	require.Len(t, batch.Observations, 1)
	require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[0].GetCollectionError().Category)
}

func TestGPMFreeFailureReplacesMetricOutcome(t *testing.T) {
	public, err := NewGPM(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": &profilingDevice{supported: 1}}}, 101*time.Millisecond, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	sensor := public.(*gpmSensor)
	sensor.alloc = func() (nvml.GpmSample, nvml.Return) { return &profilingSample{ret: nvml.ERROR_UNKNOWN}, nvml.SUCCESS }
	sensor.getMetrics = func(query *nvml.GpmMetricsGetType) nvml.Return { query.Metrics[0].Value = 50; return nvml.SUCCESS }
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 1)
	require.Nil(t, batch.Observations[0].GetValue())
	require.Equal(t, "GpmSampleFree", batch.Observations[0].GetCollectionError().Operation)
}

func TestGPMNoTargetsRespectsLifecycleGate(t *testing.T) {
	for _, closed := range []bool{false, true} {
		inst := &fakeInstance{exists: true}
		if closed {
			inst.readErr = nvmlsource.ErrClosed
		}
		sensor, err := NewGPM(inst, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
		require.NoError(t, err)
		batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
		require.NoError(t, err)
		require.Len(t, batch.Observations, 1)
		if closed {
			require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().Category)
		} else {
			require.Equal(t, observation.NoValueReasonNoTargets, batch.Observations[0].GetNoValue().Reason)
		}
	}
}

type profilingInventoryInstance struct {
	fakeInstance
	records []nvmlsource.InventoryDevice
}

func (s *profilingInventoryInstance) InventoryDevices() []nvmlsource.InventoryDevice {
	return s.records
}

func TestGPMInitialEnumerationFailureIsNotNoTargets(t *testing.T) {
	sensor, err := NewGPM(&fakeInstance{exists: true, initErr: errors.New("device count unavailable")}, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 1)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().Category)
	require.Equal(t, "GetDevices", batch.Observations[0].GetCollectionError().Operation)
}

func TestGPMUnresolvedSlotsHaveSourceLocalErrors(t *testing.T) {
	inst := &profilingInventoryInstance{fakeInstance: fakeInstance{exists: true}, records: []nvmlsource.InventoryDevice{{SourceIndex: 4, HandleReturn: nvml.ERROR_GPU_IS_LOST}}}
	sensor, err := NewGPM(inst, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 1)
	require.NotNil(t, batch.Observations[0].Target)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().Category)
	require.Nil(t, batch.Observations[0].GetNoValue())
}
