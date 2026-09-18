package nvml

import (
	"context"
	"errors"
	"fmt"
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

// sensor reads GPU power and temperature through the NVML source client.
type sensor struct {
	inst nvmlsource.Instance
	mu   sync.Mutex
}

// New returns a sensor over the given NVML instance.
func New(inst nvmlsource.Instance) collect.Sensor {
	return &sensor{inst: inst}
}

func (s *sensor) ID() string { return ID }

func (s *sensor) Signals() collect.Signals {
	return append(temperatureSignals(), powerSignals()...)
}

// Measure reads selected fields. WithCycle applies the shared collection timeout;
// calls without a cycle retain the synchronous API. Reuse one sensor per instance
// and shut the instance down only after in-flight native calls have completed.
func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	if op.GPUInventory() {
		return nil, fmt.Errorf("NVML sensor GPU inventory is not supported yet")
	}
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
	if len(selected) == 0 {
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
		case errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, context.Canceled), errors.Is(err, errInUse), errors.Is(err, nvmlsource.ErrInUse):
			category = observation.CollectionErrorCategoryUnavailable
		}
		batch = allSignalsFailed(selected, now, "Measure", category, err.Error())
	}
	batch.CollectionId = cycle.CollectionID()
	return batch, nil
}

var errInUse = errors.New("NVML read already in progress")

func (s *sensor) measure(ctx context.Context, op *collect.Op, selected collect.Signals, now *timestamppb.Timestamp) (*observation.ObservationBatch, error) {
	batch := &observation.ObservationBatch{}

	if !s.inst.NVMLExists() {
		if initErr := s.inst.InitError(); initErr != nil {
			return allSignalsFailed(selected, now, "Init", observation.CollectionErrorCategoryUnavailable, initErr.Error()), nil
		}
		return allSignalsFailed(selected, now, "Init", observation.CollectionErrorCategoryUnsupported, "NVML library not found"), nil
	}
	if op.Refresh() {
		if err := s.inst.Refresh(); err != nil {
			// Device enumeration failed this cycle (e.g. a GPU fell off the bus):
			// every selected signal is unavailable, attributed to the source.
			return allSignalsFailed(selected, now, "GetDevices", observation.CollectionErrorCategoryUnavailable, err.Error()), nil
		}
	} else if len(s.inst.Devices()) == 0 && s.inst.InitError() != nil {
		// The instantiation-time enumeration failed and no refresh has
		// recovered it, so there is no static list to measure. An empty
		// batch would read as "no GPUs, nothing wrong"; report every
		// selected signal unavailable instead.
		return allSignalsFailed(selected, now, "GetDevices", observation.CollectionErrorCategoryUnavailable, s.inst.InitError().Error()), nil
	}
	devices := s.inst.Devices()
	if len(devices) == 0 {
		appendNoTargetObservations(batch, selected, now)
		return batch, nil
	}

	want := make(map[string]bool, len(selected))
	for _, definition := range selected {
		want[definition.ID()] = true
	}
	err := s.inst.Read(func(devices map[string]device.Device) error {
		for uuid, dev := range devices {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			target := observation.GPUTarget(uuid)
			ret := measureTemperature(ctx, batch, now, target, dev, want)
			measurePower(ctx, batch, now, target, dev, want, ret)
		}
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
