// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpu "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type profilingDevice struct {
	device.Device
	block       chan struct{}
	entered     chan struct{}
	sampleCalls int
	support     nvml.Return
	supported   uint32
}

func (d *profilingDevice) GpmQueryDeviceSupport() (nvml.GpmSupport, nvml.Return) {
	return nvml.GpmSupport{IsSupportedDevice: d.supported}, d.support
}

func (d *profilingDevice) GpmSampleGet(nvml.GpmSample) nvml.Return {
	d.sampleCalls++
	if d.entered != nil {
		close(d.entered)
		d.entered = nil
		<-d.block
	}
	return nvml.SUCCESS
}

type profilingSample struct {
	nvml.GpmSample
	freed int
	ret   nvml.Return
}

func (s *profilingSample) Free() nvml.Return { s.freed++; return s.ret }

func profilingCycle(t *testing.T, timeout time.Duration) *collect.Cycle {
	t.Helper()
	cycle, err := collect.NewCycleWithTimeout("profile-cycle", time.Now(), timeout)
	require.NoError(t, err)
	return cycle
}

func TestGPMConstructorAndEmptySelection(t *testing.T) {
	inst := &fakeInstance{exists: true}
	for _, duration := range []time.Duration{-1, 0, 100 * time.Millisecond} {
		_, err := NewGPM(inst, duration)
		require.Error(t, err)
	}
	_, err := NewGPM(inst, time.Second, nvml.GpmMetricId(-1))
	require.Error(t, err)
	_, err = NewGPM(inst, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.Error(t, err)
	s, err := NewGPM(inst, time.Second)
	require.NoError(t, err)
	batch, err := s.Measure(context.Background(), collect.WithSignalID())
	require.NoError(t, err)
	require.Empty(t, batch.Observations)
	_, err = s.Measure(context.Background())
	require.Error(t, err)
}

func TestGPMSelectedSamplesAndMetricFailures(t *testing.T) {
	dev := &profilingDevice{supported: 1}
	public, err := NewGPM(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}}, 101*time.Millisecond, nvml.GPM_METRIC_SM_OCCUPANCY, nvml.GPM_METRIC_FP64_UTIL, nvml.GPM_METRIC_FP32_UTIL)
	require.NoError(t, err)
	s := public.(*gpmSensor)
	first, second := &profilingSample{}, &profilingSample{}
	allocations := 0
	s.alloc = func() (nvml.GpmSample, nvml.Return) {
		allocations++
		if allocations == 1 {
			return first, nvml.SUCCESS
		}
		return second, nvml.SUCCESS
	}
	s.getMetrics = func(query *nvml.GpmMetricsGetType) nvml.Return {
		require.Equal(t, uint32(3), query.NumMetrics)
		require.Equal(t, uint32(nvml.GPM_METRIC_SM_OCCUPANCY), query.Metrics[0].MetricId)
		query.Metrics[0].Value = 75.5
		query.Metrics[1].NvmlReturn = uint32(nvml.ERROR_NO_PERMISSION)
		query.Metrics[2].Value = math.NaN()
		return nvml.SUCCESS
	}
	batch, err := s.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Len(t, batch.Observations, 3)
	require.Equal(t, 2, dev.sampleCalls)
	require.Equal(t, 1, first.freed)
	require.Equal(t, 1, second.freed)
	metric := batch.Observations[0]
	require.Equal(t, 75.5, metric.GetValue().GetDoubleValue())
	require.Equal(t, "percent", metric.GetUnit())
	require.Greater(t, metric.SampleEndedAt.AsTime().Sub(metric.SampleStartedAt.AsTime()), 100*time.Millisecond)
	require.Equal(t, "profile-cycle", batch.CollectionId)
	require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, batch.Observations[1].GetCollectionError().Category)
	require.Equal(t, observation.CollectionErrorCategoryParseError, batch.Observations[2].GetCollectionError().Category)
}

func TestGPMUnsupportedDoesNotAllocate(t *testing.T) {
	for _, ret := range []nvml.Return{nvml.SUCCESS, nvml.ERROR_NO_PERMISSION, nvml.ERROR_ARGUMENT_VERSION_MISMATCH} {
		sensor, err := NewGPM(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": &profilingDevice{support: ret}}}, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
		require.NoError(t, err)
		sensor.(*gpmSensor).alloc = func() (nvml.GpmSample, nvml.Return) {
			t.Fatal("unsupported query allocated a sample")
			return nil, nvml.ERROR_UNKNOWN
		}
		batch, err := sensor.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
		require.NoError(t, err)
		require.Len(t, batch.Observations, 1)
		require.NotNil(t, batch.Observations[0].GetCollectionError())
	}
}

func TestGPMTimeoutPreventsNativeReentry(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	dev := &profilingDevice{supported: 1, entered: entered, block: release}
	sensor, err := NewGPM(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}}, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	s := sensor.(*gpmSensor)
	var freeWG sync.WaitGroup
	freeWG.Add(2)
	s.alloc = func() (nvml.GpmSample, nvml.Return) { return &completionSample{done: &freeWG}, nvml.SUCCESS }
	result := make(chan *observation.ObservationBatch, 1)
	cycle := profilingCycle(t, 50*time.Millisecond)
	go func() { batch, _ := s.Measure(context.Background(), collect.WithCycle(cycle)); result <- batch }()
	<-entered
	batch := <-result
	require.Equal(t, observation.CollectionErrorCategoryTimeout, batch.Observations[0].GetCollectionError().Category)
	batch, err = s.Measure(context.Background(), collect.WithCycle(profilingCycle(t, time.Second)))
	require.NoError(t, err)
	require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[0].GetCollectionError().Category)
	close(release)
	freeWG.Wait()
}

type completionSample struct {
	nvml.GpmSample
	done *sync.WaitGroup
}

func (s *completionSample) Free() nvml.Return { s.done.Done(); return nvml.SUCCESS }

func TestGPMSelectionRejectsUnknown(t *testing.T) {
	s, err := NewGPM(&fakeInstance{exists: true}, time.Second, nvml.GPM_METRIC_SM_OCCUPANCY)
	require.NoError(t, err)
	_, err = s.Measure(context.Background(), collect.WithSignalID(gpu.GPMFP64Utilization.ID()))
	require.Error(t, err)
}
