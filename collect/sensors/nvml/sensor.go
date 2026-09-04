package nvml

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
)

var _ collect.Sensor = (*sensor)(nil)

// ID identifies the NVML-backed GPU sensor.
const ID = "gpu.nvml"

// sensor reads GPU temperature through the NVML source client.
type sensor struct {
	inst nvmlsource.Instance
}

// New returns a sensor over the given NVML instance.
func New(inst nvmlsource.Instance) collect.Sensor {
	return &sensor{inst: inst}
}

func (s *sensor) ID() string { return ID }

func (s *sensor) Signals() collect.Signals {
	return temperatureSignals()
}

// Measure emits one observation per selected (signal, GPU). Unselected
// signals are never queried. By default the device list captured at
// instantiation is measured as-is; WithRefresh(true) re-enumerates devices
// for this call (the per-cycle shared read until the coordinator lands).
//
// TODO: run the NVML calls through collect.Run, as the DCGM source does. A
// hung NVML cgo call currently blocks Measure with no timeout.
func (s *sensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, fmt.Errorf("select NVML signals: %w", err)
	}
	now := timestamppb.Now()
	batch := &observation.ObservationBatch{}

	if !s.inst.NVMLExists() {
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

	want := make(map[string]bool, len(selected))
	for _, definition := range selected {
		want[definition.ID()] = true
	}
	for uuid, dev := range s.inst.Devices() {
		entity := &observation.Entity{Type: "gpu", Id: uuid}
		measureTemperature(batch, now, entity, dev, want)
	}
	return batch, nil
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
