package nvml

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpu "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type telemetryDevice struct {
	device.Device
	clocks      []nvml.ClockType
	memoryCalls int
	oldCalls    int
	memoryRet   nvml.Return
}

func (d *telemetryDevice) GetMemoryInfo_v2() (nvml.Memory_v2, nvml.Return) {
	d.memoryCalls++
	return nvml.Memory_v2{Total: 4 * 1024 * 1024, Free: 1024 * 1024, Used: 3 * 1024 * 1024, Reserved: 512 * 1024}, d.memoryRet
}

func (d *telemetryDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	d.oldCalls++
	return nvml.Memory{Total: 4 * 1024 * 1024, Free: 1024 * 1024, Used: 3 * 1024 * 1024}, nvml.SUCCESS
}

func (d *telemetryDevice) GetClockInfo(kind nvml.ClockType) (uint32, nvml.Return) {
	d.clocks = append(d.clocks, kind)
	return 1000 + uint32(kind), nvml.SUCCESS
}

func (d *telemetryDevice) GetCurrentClocksEventReasons() (uint64, nvml.Return) {
	return uint64(1)<<63 | nvml.ClocksThrottleReasonHwSlowdown, nvml.SUCCESS
}

func TestTelemetrySelectedMemoryUnitsAndFallback(t *testing.T) {
	for _, ret := range []nvml.Return{nvml.SUCCESS, nvml.ERROR_FUNCTION_NOT_FOUND, nvml.ERROR_NO_PERMISSION} {
		t.Run(returnName(ret), func(t *testing.T) {
			dev := &telemetryDevice{memoryRet: ret}
			s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}})
			batch, err := s.Measure(context.Background(), collect.WithSignalID(gpu.FramebufferUsed.ID(), gpu.FramebufferReserved.ID(), gpu.FramebufferUsedRatio.ID()))
			require.NoError(t, err)
			require.Len(t, batch.Observations, 3)
			require.Equal(t, 1, dev.memoryCalls)
			if ret == nvml.ERROR_NO_PERMISSION {
				require.Zero(t, dev.oldCalls)
				require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[0].GetCollectionError().Category)
				return
			}
			require.Equal(t, float64(3), batch.Observations[0].GetValue().GetDoubleValue())
			require.Equal(t, float64(.75), batch.Observations[2].GetValue().GetDoubleValue())
			if ret == nvml.SUCCESS {
				require.Equal(t, float64(.5), batch.Observations[1].GetValue().GetDoubleValue())
			} else {
				require.Equal(t, observation.CollectionErrorCategoryUnsupported, batch.Observations[1].GetCollectionError().Category)
			}
		})
	}
}

func TestTelemetryClockIdentityAndUnsignedReasons(t *testing.T) {
	dev := &telemetryDevice{}
	s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}})
	batch, err := s.Measure(context.Background(), collect.WithSignalID(gpu.GraphicsClock.ID(), gpu.SMClock.ID(), gpu.ClockEventReasons.ID(), gpu.HardwareSlowdownActive.ID()))
	require.NoError(t, err)
	require.Equal(t, []nvml.ClockType{nvml.CLOCK_GRAPHICS, nvml.CLOCK_SM}, dev.clocks)
	require.Equal(t, uint64(1000), batch.Observations[0].GetValue().GetUintValue())
	require.Equal(t, uint64(1001), batch.Observations[1].GetValue().GetUintValue())
	require.Equal(t, uint64(1)<<63|8, batch.Observations[2].GetValue().GetUintValue())
	require.True(t, batch.Observations[3].GetValue().GetBoolValue())
}

func TestTelemetryLostGPUStopsNativeQueries(t *testing.T) {
	dev := &telemetryDevice{memoryRet: nvml.ERROR_GPU_IS_LOST}
	batch := &observation.ObservationBatch{}
	measureTelemetry(context.Background(), batch, timestamppb.Now(), observation.GPUTarget("gpu"), dev, map[string]bool{gpu.FramebufferUsed.ID(): true, gpu.SMClock.ID(): true})
	require.Len(t, batch.Observations, 2)
	require.Zero(t, dev.oldCalls)
	require.Empty(t, dev.clocks)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[1].GetCollectionError().Category)
}

func TestUnifiedMemoryParsing(t *testing.T) {
	memory, err := readUnifiedMemory(fstest.MapFS{"proc/meminfo": {Data: []byte("MemTotal: 1024 kB\nMemFree: 128 kB\nMemAvailable: 256 kB\n")}})
	require.NoError(t, err)
	require.Equal(t, uint64(128*1024), memory.Free)
	require.Equal(t, uint64(768*1024), memory.Used)
	for _, text := range []string{"MemTotal: 1 kB\n", "MemTotal: 1 kB\nMemTotal: 1 kB\nMemFree: 0 kB\nMemAvailable: 0 kB\n", "MemTotal: 1 kB\nMemFree: 0 kB\nMemAvailable: 2 kB\n", "MemTotal: 18446744073709551615 kB\n"} {
		_, err := readUnifiedMemory(fstest.MapFS{"proc/meminfo": {Data: []byte(text)}})
		require.Error(t, err)
	}
}
