// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

var _ collect.Sensor = (*sensor)(nil)

// ID identifies the NVML-backed GPU sensor.
const ID = "gpu.nvml"

// sensor reads selected GPU fields through one NVML instance.
type sensor struct {
	inst           nvmlsource.Instance
	fsys           fs.FS
	processCmdline bool
	mu             sync.Mutex
}

// New returns a sensor over the given NVML instance.
func New(inst nvmlsource.Instance) collect.Sensor {
	return NewWithOptions(inst)
}

// Option configures optional host-process diagnostics.
type Option func(*sensor)

// WithProcessFilesystem selects the filesystem used for process metadata.
func WithProcessFilesystem(fsys fs.FS) Option { return func(s *sensor) { s.fsys = fsys } }

// WithProcessCmdlineDiagnostics enables bounded command-line reads.
// Reads may block in the kernel; a timed-out read keeps the native gate held.
func WithProcessCmdlineDiagnostics(enabled bool) Option {
	return func(s *sensor) { s.processCmdline = enabled }
}

// NewWithOptions creates a sensor with explicit diagnostic options.
func NewWithOptions(inst nvmlsource.Instance, opts ...Option) collect.Sensor {
	s := &sensor{inst: inst}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *sensor) ID() string { return ID }

func (s *sensor) Signals() collect.Signals {
	signals := slices.Concat(temperatureSignals(), powerSignals(), reliabilitySignals(), interconnectSignals(), telemetrySignals(), processSignals())
	if s.processCmdline {
		signals = append(signals, processDiagnosticSignals()...)
	}
	return signals
}

// Measure reads selected fields. WithCycle applies the shared collection timeout;
// calls without a cycle retain the synchronous API. Reuse one sensor per instance
// and shut the instance down only after in-flight native calls have completed.
func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, fmt.Errorf("select NVML signals: %w", err)
	}
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	now := timestamppb.Now()
	cycle := op.Cycle()
	if cycle != nil {
		now = timestamppb.New(cycle.StartedAt())
	}
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	if len(selected) == 0 && !op.GPUInventory() {
		return batch, nil
	}
	if s.inst == nil {
		return nil, fmt.Errorf("NVML instance is required")
	}
	read := func(ctx context.Context) (*observation.ObservationBatch, error) {
		if !s.mu.TryLock() {
			return nil, errInUse
		}
		defer s.mu.Unlock()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return s.measure(ctx, op, selected, now)
	}
	if cycle == nil {
		batch, err = read(ctx)
	} else {
		batch, err = collect.Run(ctx, cycle, source, read)
	}
	if err != nil {
		category := observation.CollectionErrorCategorySourceError
		switch {
		case errors.Is(err, collect.ErrCollectionTimeout), errors.Is(err, context.DeadlineExceeded):
			category = observation.CollectionErrorCategoryTimeout
		case errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, context.Canceled), errors.Is(err, errInUse), errors.Is(err, nvmlsource.ErrInUse), errors.Is(err, nvmlsource.ErrClosed):
			category = observation.CollectionErrorCategoryUnavailable
		}
		batch = allSignalsFailed(selected, now, "Measure", category, err.Error())
		if op.GPUInventory() {
			appendInventoryFailure(batch, now, "Measure", category, err.Error())
		}
	}
	batch.CollectionId = cycle.CollectionID()
	return batch, nil
}

var errInUse = errors.New("NVML read already in progress")

func (s *sensor) measure(ctx context.Context, op *collect.Op, selected collect.Signals, now *timestamppb.Timestamp) (*observation.ObservationBatch, error) {
	batch := &observation.ObservationBatch{}
	fail := func(operation string, category observation.CollectionErrorCategory, detail string) *observation.ObservationBatch {
		failed := allSignalsFailed(selected, now, operation, category, detail)
		if op.GPUInventory() {
			appendInventoryFailure(failed, now, operation, category, detail)
		}
		return failed
	}

	if !s.inst.NVMLExists() {
		if initErr := s.inst.InitError(); initErr != nil {
			return fail("Init", observation.CollectionErrorCategoryUnavailable, initErr.Error()), nil
		}
		return fail("Init", observation.CollectionErrorCategoryUnsupported, "NVML library not found"), nil
	}
	if op.Refresh() {
		if err := s.inst.Refresh(); err != nil {
			// Device enumeration failed this cycle (e.g. a GPU fell off the bus):
			// every selected signal is unavailable, attributed to the source.
			return fail("GetDevices", observation.CollectionErrorCategoryUnavailable, err.Error()), nil
		}
	} else if initErr := s.inst.InitError(); initErr != nil && len(s.inst.Devices()) == 0 {
		// The instantiation-time enumeration failed and no refresh has
		// recovered it, so there is no static list to measure. An empty
		// batch would read as "no GPUs, nothing wrong"; report every
		// selected signal unavailable instead.
		return fail("GetDevices", observation.CollectionErrorCategoryUnavailable, initErr.Error()), nil
	}

	want := make(map[string]bool, len(selected))
	for _, definition := range selected {
		want[definition.ID()] = true
	}
	err := s.inst.Read(func(devices map[string]device.Device) error {
		var records []nvmlsource.InventoryDevice
		if provider, ok := s.inst.(interface {
			InventoryDevices() []nvmlsource.InventoryDevice
		}); ok {
			records = provider.InventoryDevices()
		}
		if op.GPUInventory() {
			input := inventoryInput{Devices: records}
			if provider, ok := s.inst.(interface{ Metadata() nvmlsource.Metadata }); ok {
				metadata := provider.Metadata()
				input.Metadata = &metadata
			}
			batch.Inventory = &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{measureInventory(ctx, now, devices, input)}}
		}
		for _, record := range records {
			if record.Device != nil && record.UUIDReturn == 0 && record.Error == nil {
				continue
			}
			detail := "GPU identity or handle is unavailable"
			if record.Error != nil {
				detail = record.Error.Error()
			}
			target := observation.GPUSourceLocalTarget(fmt.Sprint(record.SourceIndex))
			for _, signal := range selected {
				batch.Observations = append(batch.Observations, errorObservation(now, target, signal, "GetDevices", observation.CollectionErrorCategoryUnavailable, "", detail))
			}
		}
		if len(devices) == 0 {
			if len(records) == 0 {
				appendNoTargetObservations(batch, selected, now)
			}
			return ctx.Err()
		}
		lost := make(map[string]bool)
		for uuid, dev := range devices {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			target := observation.GPUTarget(uuid)
			ret := measureTemperature(ctx, batch, now, target, dev, want)
			measurePower(ctx, batch, now, target, dev, want, ret)
			if appendFatalFailures(batch, now, target, selected) {
				lost[uuid] = true
				continue
			}
			measureReliability(ctx, batch, now, target, dev, want)
			if appendFatalFailures(batch, now, target, selected) {
				lost[uuid] = true
				continue
			}
			measureInterconnect(ctx, batch, now, target, dev, want)
			if appendFatalFailures(batch, now, target, selected) {
				lost[uuid] = true
				continue
			}
			measureTelemetryFS(ctx, batch, now, target, dev, want, s.fsys)
			if appendFatalFailures(batch, now, target, selected) {
				lost[uuid] = true
				continue
			}
			measureProcessesWithDiagnostics(ctx, batch, now, target, dev, want, s.fsys, s.processCmdline)
			if appendFatalFailures(batch, now, target, selected) {
				lost[uuid] = true
			}
		}
		measurePeers(ctx, batch, now, devices, lost, want)
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	return batch, nil
}

func appendNoTargetObservations(batch *observation.ObservationBatch, selected collect.Signals, now *timestamppb.Timestamp) {
	for _, definition := range selected {
		batch.Observations = append(batch.Observations, observation.NewNoValueObservation(
			definition.ID(),
			definition.Unit(),
			source,
			nil,
			now,
			&observation.NoValue{
				Reason: observation.NoValueReasonNoTargets,
				Detail: "No GPUs detected",
			},
		))
	}
}

// allSignalsFailed emits a collection-error observation for every selected
// signal, when the failure is above the device level.
func allSignalsFailed(selected collect.Signals, now *timestamppb.Timestamp, operation string, category observation.CollectionErrorCategory, detail string) *observation.ObservationBatch {
	batch := &observation.ObservationBatch{}
	for _, definition := range selected {
		batch.Observations = append(batch.Observations, errorObservation(now, nil, definition, operation, category, "", detail))
	}
	return batch
}

func appendInventoryFailure(batch *observation.ObservationBatch, now *timestamppb.Timestamp, operation string, category observation.CollectionErrorCategory, detail string) {
	batch.Inventory = &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{{Source: source, CollectedAt: now, Outcome: &observation.GPUInventorySnapshot_CollectionError{CollectionError: &observation.CollectionError{Source: source, Operation: operation, Category: category, Detail: detail}}}}}
}

// Device loss ends native queries, but every selected field retains an outcome.
func appendFatalFailures(batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, selected collect.Signals) bool {
	start := len(batch.Observations)
	var fatal *observation.CollectionError
	for _, obs := range batch.Observations {
		if observationGPUUUID(obs.GetTarget()) != target.GetGpu().GetUuid() {
			continue
		}
		failure := obs.GetCollectionError()
		if failure.GetSourceError() == "ERROR_GPU_IS_LOST" || failure.GetSourceError() == "ERROR_RESET_REQUIRED" {
			fatal = failure
		}
	}
	if fatal == nil {
		return false
	}
	seen := make(map[string]bool)
	for _, obs := range batch.Observations[:start] {
		if observationGPUUUID(obs.GetTarget()) == target.GetGpu().GetUuid() {
			seen[obs.GetSignalId()] = true
		}
	}
	for _, signal := range selected {
		if !seen[signal.ID()] {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(), source, fatal.GetOperation(), target, now, fatal))
		}
	}
	return true
}

func observationGPUUUID(target *observation.ObservationTarget) string {
	if gpu := target.GetGpu(); gpu != nil {
		return gpu.GetUuid()
	}
	if endpoint := target.GetNvLinkEndpoint(); endpoint != nil {
		return endpoint.GetGpu().GetUuid()
	}
	return target.GetGpuProcess().GetGpu().GetUuid()
}
