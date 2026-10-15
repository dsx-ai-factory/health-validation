// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpu "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

const GPMID = "gpu.nvml.gpm"

var gpmDefinitions = []struct {
	signal collect.Signal
	id     nvml.GpmMetricId
}{
	{id: nvml.GPM_METRIC_SM_OCCUPANCY, signal: gpu.GPMSMOccupancy},
	{id: nvml.GPM_METRIC_INTEGER_UTIL, signal: gpu.GPMIntegerUtilization},
	{id: nvml.GPM_METRIC_ANY_TENSOR_UTIL, signal: gpu.GPMTensorUtilization},
	{id: nvml.GPM_METRIC_DFMA_TENSOR_UTIL, signal: gpu.GPMDFMATensorUtilization},
	{id: nvml.GPM_METRIC_HMMA_TENSOR_UTIL, signal: gpu.GPMHMMATensorUtilization},
	{id: nvml.GPM_METRIC_IMMA_TENSOR_UTIL, signal: gpu.GPMIMMATensorUtilization},
	{id: nvml.GPM_METRIC_FP64_UTIL, signal: gpu.GPMFP64Utilization},
	{id: nvml.GPM_METRIC_FP32_UTIL, signal: gpu.GPMFP32Utilization},
	{id: nvml.GPM_METRIC_FP16_UTIL, signal: gpu.GPMFP16Utilization},
}

type gpmSensor struct {
	inst       nvmlsource.Instance
	alloc      func() (nvml.GpmSample, nvml.Return)
	getMetrics func(*nvml.GpmMetricsGetType) nvml.Return
	wait       func(context.Context, time.Duration) error
	metrics    []nvml.GpmMetricId
	duration   time.Duration
	mu         sync.Mutex
}

// NewGPM creates a read-only profiling sensor. NVML requires more than 100 ms
// between sample reads. An empty metric list selects GPUd's nine metric families.
// Reuse this sensor and instance; timeout does not interrupt an active cgo call.
// Sampling holds the process-wide NVML gate through the wait and sample frees.
// Do not overlap NVML collection, Refresh, or Shutdown, even on another instance:
// they return ErrInUse until sampling and cleanup finish. A timeout only stops waiting.
func NewGPM(inst nvmlsource.Instance, sampleDuration time.Duration, metricIDs ...nvml.GpmMetricId) (collect.Sensor, error) {
	if inst == nil {
		return nil, fmt.Errorf("NVML instance is required")
	}
	if sampleDuration <= 100*time.Millisecond {
		return nil, fmt.Errorf("GPM sample duration must be greater than 100ms")
	}
	if len(metricIDs) == 0 {
		for _, metric := range gpmDefinitions {
			metricIDs = append(metricIDs, metric.id)
		}
	}
	known := make(map[nvml.GpmMetricId]bool, len(gpmDefinitions))
	for _, metric := range gpmDefinitions {
		known[metric.id] = true
	}
	seen := make(map[nvml.GpmMetricId]bool, len(metricIDs))
	for _, id := range metricIDs {
		if !known[id] {
			return nil, fmt.Errorf("unsupported GPM metric ID %d", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate GPM metric ID %d", id)
		}
		seen[id] = true
	}
	return &gpmSensor{inst: inst, duration: sampleDuration, metrics: append([]nvml.GpmMetricId(nil), metricIDs...), alloc: nvml.GpmSampleAlloc, getMetrics: nvml.GpmMetricsGet}, nil
}

func (s *gpmSensor) ID() string { return GPMID }
func (s *gpmSensor) Signals() collect.Signals {
	result := make(collect.Signals, 0, len(s.metrics))
	for _, metric := range gpmDefinitions {
		for _, id := range s.metrics {
			if id == metric.id {
				result = append(result, metric.signal)
				break
			}
		}
	}
	return result
}

func (s *gpmSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	if op.GPUInventory() {
		return nil, fmt.Errorf("GPM sensor does not collect GPU inventory")
	}
	cycle := op.Cycle()
	now := timestamppb.Now()
	if cycle != nil {
		now = timestamppb.New(cycle.StartedAt())
	}
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	if cycle == nil {
		return nil, fmt.Errorf("GPM collection requires WithCycle to bound blocking native calls")
	}
	batch, err = collect.Run(ctx, cycle, source, func(ctx context.Context) (*observation.ObservationBatch, error) {
		if !s.mu.TryLock() {
			return nil, errInUse
		}
		defer s.mu.Unlock()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !s.inst.NVMLExists() {
			if initErr := s.inst.InitError(); initErr != nil {
				return allSignalsFailed(selected, now, "Init", observation.CollectionErrorCategoryUnavailable, initErr.Error()), nil
			}
			return allSignalsFailed(selected, now, "Init", observation.CollectionErrorCategoryUnsupported, "NVML library not found"), nil
		}
		if op.Refresh() {
			if refreshErr := s.inst.Refresh(); refreshErr != nil {
				return nil, refreshErr
			}
		}
		if initErr := s.inst.InitError(); initErr != nil && len(s.inst.Devices()) == 0 {
			return allSignalsFailed(selected, now, "GetDevices", observation.CollectionErrorCategoryUnavailable, initErr.Error()), nil
		}
		out := &observation.ObservationBatch{}
		readErr := s.inst.Read(func(devices map[string]device.Device) error {
			var records []nvmlsource.InventoryDevice
			if provider, ok := s.inst.(interface {
				InventoryDevices() []nvmlsource.InventoryDevice
			}); ok {
				records = provider.InventoryDevices()
			}
			for _, record := range records {
				if record.Device != nil && record.UUIDReturn == nvml.SUCCESS && record.Error == nil {
					continue
				}
				detail := "GPU identity or handle is unavailable"
				if record.Error != nil {
					detail = record.Error.Error()
				}
				target := observation.GPUSourceLocalTarget(fmt.Sprint(record.SourceIndex))
				for _, signal := range selected {
					out.Observations = append(out.Observations, errorObservation(now, target, signal, "GetDevices", observation.CollectionErrorCategoryUnavailable, "", detail))
				}
			}
			if len(devices) == 0 {
				if len(records) == 0 {
					appendNoTargetObservations(out, selected, now)
				}
				return ctx.Err()
			}
			return s.sampleDevices(ctx, out, now, devices, selected)
		})
		return out, readErr
	})
	if err != nil {
		category := observation.CollectionErrorCategorySourceError
		if errors.Is(err, collect.ErrCollectionTimeout) || errors.Is(err, context.DeadlineExceeded) {
			category = observation.CollectionErrorCategoryTimeout
		} else if errors.Is(err, errInUse) || errors.Is(err, nvmlsource.ErrInUse) || errors.Is(err, nvmlsource.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, collect.ErrCollectionCanceled) {
			category = observation.CollectionErrorCategoryUnavailable
		}
		batch = allSignalsFailed(selected, now, "GPM", category, err.Error())
	}
	batch.CollectionId = cycle.CollectionID()
	return batch, nil
}

type gpmPair struct {
	started time.Time
	dev     device.Device
	first   nvml.GpmSample
	second  nvml.GpmSample
	target  *observation.ObservationTarget
	batch   observation.ObservationBatch
}

func waitGPM(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// sampleDevices keeps every native query and sample free under Instance.Read.
// All first samples precede a single wait; second samples remain sequential.
func (s *gpmSensor) sampleDevices(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, devices map[string]device.Device, selected collect.Signals) error {
	pairs := make([]*gpmPair, 0, len(devices))
	fail := func(pair *gpmPair, operation string, ret nvml.Return) {
		for _, signal := range selected {
			pair.batch.Observations = append(pair.batch.Observations, errorObservation(now, pair.target, signal, operation, categorizeFailure(signal, ret), returnName(ret), nvml.ErrorString(ret)))
		}
	}
	defer func() {
		for _, pair := range pairs {
			freeRet := nvml.SUCCESS
			if pair.second != nil {
				freeRet = pair.second.Free()
			}
			if pair.first != nil {
				if ret := pair.first.Free(); ret != nvml.SUCCESS {
					freeRet = ret
				}
			}
			if freeRet != nvml.SUCCESS {
				pair.batch.Observations = nil
				fail(pair, "GpmSampleFree", freeRet)
			}
			batch.Observations = append(batch.Observations, pair.batch.Observations...)
		}
	}()
	ready := 0
	for uuid, dev := range devices {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pair := &gpmPair{dev: dev, target: observation.GPUTarget(uuid)}
		pairs = append(pairs, pair)
		support, ret := dev.GpmQueryDeviceSupport()
		if ret != nvml.SUCCESS {
			fail(pair, "GpmQueryDeviceSupport", ret)
			continue
		}
		if support.IsSupportedDevice == 0 {
			fail(pair, "GpmQueryDeviceSupport", nvml.ERROR_NOT_SUPPORTED)
			continue
		}
		pair.first, ret = s.alloc()
		if ret != nvml.SUCCESS {
			pair.first = nil
			fail(pair, "GpmSampleAlloc", ret)
			continue
		}
		pair.second, ret = s.alloc()
		if ret != nvml.SUCCESS {
			pair.second = nil
			fail(pair, "GpmSampleAlloc", ret)
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ret = dev.GpmSampleGet(pair.first); ret != nvml.SUCCESS {
			fail(pair, "GpmSampleGet", ret)
			continue
		}
		pair.started = time.Now()
		ready++
	}
	if ready == 0 {
		return ctx.Err()
	}
	wait := s.wait
	if wait == nil {
		wait = waitGPM
	}
	if err := wait(ctx, s.duration); err != nil {
		return err
	}
	for _, pair := range pairs {
		if pair.started.IsZero() {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ret := pair.dev.GpmSampleGet(pair.second)
		ended := time.Now()
		if ret != nvml.SUCCESS {
			fail(pair, "GpmSampleGet", ret)
			continue
		}
		query := nvml.GpmMetricsGetType{NumMetrics: uint32(len(selected)), Sample1: pair.first, Sample2: pair.second}
		for i, signal := range selected {
			for _, metric := range gpmDefinitions {
				if signal == metric.signal {
					query.Metrics[i].MetricId = uint32(metric.id)
					break
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ret = s.getMetrics(&query)
		if ret != nvml.SUCCESS {
			fail(pair, "GpmMetricsGet", ret)
		} else if query.NumMetrics != uint32(len(selected)) {
			fail(pair, "GpmMetricsGet", nvml.ERROR_INVALID_ARGUMENT)
		} else {
			for i, signal := range selected {
				metric := query.Metrics[i]
				var expectedID uint32
				for _, definition := range gpmDefinitions {
					if signal == definition.signal {
						expectedID = uint32(definition.id)
						break
					}
				}
				if metric.MetricId != expectedID {
					pair.batch.Observations = append(pair.batch.Observations, errorObservation(now, pair.target, signal, "GpmMetricsGet", observation.CollectionErrorCategoryParseError, "", fmt.Sprintf("returned metric ID %d; expected %d", metric.MetricId, expectedID)))
					continue
				}
				ret := nvml.Return(metric.NvmlReturn)
				if ret != nvml.SUCCESS {
					pair.batch.Observations = append(pair.batch.Observations, errorObservation(now, pair.target, signal, "GpmMetricsGet", categorizeFailure(signal, ret), returnName(ret), nvml.ErrorString(ret)))
					continue
				}
				if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
					pair.batch.Observations = append(pair.batch.Observations, errorObservation(now, pair.target, signal, "GpmMetricsGet", observation.CollectionErrorCategoryParseError, "", "non-finite profiling sample"))
					continue
				}
				telemetryValue(&pair.batch, now, pair.target, signal, telemetryDouble(metric.Value))
			}
		}
		for _, current := range pair.batch.Observations {
			current.SampleStartedAt = timestamppb.New(pair.started)
			current.SampleEndedAt = timestamppb.New(ended)
		}
	}
	return ctx.Err()
}
