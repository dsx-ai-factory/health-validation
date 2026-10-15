package nvml

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpu "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

type boundaryTelemetryDevice struct {
	telemetryDevice
	memory      nvml.Memory_v2
	unsupported bool
	util        nvml.Utilization
	clock       uint32
	state       nvml.Pstates
}

func (d *boundaryTelemetryDevice) GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return) {
	if d.unsupported {
		return nvml.Memory_v2{}, nvml.ERROR_NOT_SUPPORTED
	}
	return d.memory, nvml.SUCCESS
}

func (d *boundaryTelemetryDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	return nvml.Memory{}, nvml.ERROR_NOT_SUPPORTED
}
func (d *boundaryTelemetryDevice) IsCoherent() (bool, error) { return true, nil }
func (d *boundaryTelemetryDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	return d.util, nvml.SUCCESS
}

func (d *boundaryTelemetryDevice) GetClockInfo(nvml.ClockType) (uint32, nvml.Return) {
	return d.clock, nvml.SUCCESS
}

func (d *boundaryTelemetryDevice) GetPerformanceState() (nvml.Pstates, nvml.Return) {
	return d.state, nvml.SUCCESS
}

func TestTelemetryUnifiedMemoryUsesSuppliedFilesystem(t *testing.T) {
	batch := &observation.ObservationBatch{}
	measureTelemetryFS(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("gpu"), &boundaryTelemetryDevice{unsupported: true}, map[string]bool{gpu.FramebufferTotal.ID(): true, gpu.FramebufferFree.ID(): true, gpu.FramebufferUsed.ID(): true, gpu.UnifiedMemory.ID(): true}, fstest.MapFS{
		"proc/meminfo": {Data: []byte("MemTotal: 4096 kB\nMemFree: 1024 kB\nMemAvailable: 2048 kB\n")},
	})
	require.Len(t, batch.Observations, 4)
	require.Equal(t, float64(4), batch.Observations[0].GetValue().GetDoubleValue())
	require.Equal(t, float64(1), batch.Observations[1].GetValue().GetDoubleValue())
	require.Equal(t, float64(2), batch.Observations[2].GetValue().GetDoubleValue())
	require.True(t, batch.Observations[3].GetValue().GetBoolValue())
}

func TestTelemetryImpossibleNativeMemoryIsParseFailure(t *testing.T) {
	for _, memory := range []nvml.Memory_v2{{Total: 1, Free: 2}, {Total: 1, Used: 2}, {Total: 1, Reserved: 2}} {
		batch := &observation.ObservationBatch{}
		measureTelemetry(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("gpu"), &boundaryTelemetryDevice{memory: memory}, map[string]bool{gpu.FramebufferTotal.ID(): true, gpu.FramebufferUsed.ID(): true})
		require.Len(t, batch.Observations, 2)
		for _, current := range batch.Observations {
			require.Nil(t, current.GetValue())
			require.Equal(t, observation.CollectionErrorCategoryParseError, current.GetCollectionError().Category)
		}
	}
	for _, memory := range []nvml.Memory_v2{{Total: 4, Reserved: 4}, {Total: 4, Reserved: 2, Used: 3}} {
		batch := &observation.ObservationBatch{}
		measureTelemetry(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("gpu"), &boundaryTelemetryDevice{memory: memory}, map[string]bool{gpu.FramebufferUsedRatio.ID(): true})
		require.Len(t, batch.Observations, 1)
		require.Nil(t, batch.Observations[0].GetValue())
		require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[0].GetCollectionError().Category)
	}
}

func TestTelemetryInvalidNumericValuesDoNotBecomeUnsignedSuccess(t *testing.T) {
	batch := &observation.ObservationBatch{}
	measureTelemetry(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("gpu"), &boundaryTelemetryDevice{util: nvml.Utilization{Gpu: 101, Memory: 50}, clock: ^uint32(0), state: nvml.Pstates(-1)}, map[string]bool{gpu.ComputeUtilization.ID(): true, gpu.MemoryCopyUtilization.ID(): true, gpu.SMClock.ID(): true, gpu.PerformanceState.ID(): true})
	require.Len(t, batch.Observations, 4)
	require.Equal(t, uint64(50), batch.Observations[1].GetValue().GetUintValue())
	for _, index := range []int{0, 2, 3} {
		require.Nil(t, batch.Observations[index].GetValue())
		require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[index].GetCollectionError().Category)
	}
}

type failedCoherenceDevice struct{ boundaryTelemetryDevice }

func (*failedCoherenceDevice) IsCoherent() (bool, error) {
	return false, errors.New("native addressing-mode query failed")
}

func TestTelemetryCoherenceFailureIsSourceError(t *testing.T) {
	batch := &observation.ObservationBatch{}
	dev := &failedCoherenceDevice{boundaryTelemetryDevice: boundaryTelemetryDevice{unsupported: true}}
	measureTelemetry(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("gpu"), dev, map[string]bool{gpu.FramebufferTotal.ID(): true})
	require.Len(t, batch.Observations, 1)
	require.Nil(t, batch.Observations[0].GetValue())
	require.Equal(t, observation.CollectionErrorCategorySourceError, batch.Observations[0].GetCollectionError().Category)
	require.Equal(t, "IsCoherent", batch.Observations[0].GetCollectionError().Operation)
}
