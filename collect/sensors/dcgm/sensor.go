package dcgm

import (
	"context"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

var _ collect.Sensor = (*sensor)(nil)

// ID identifies the DCGM-backed GPU sensor.
const ID = "gpu.dcgm"

// sensor turns a configured set of DCGM GPU fields into observations.
type sensor struct {
	source  *dcgmsource.Source
	signals collect.Signals
}

// New creates a DCGM sensor and registers its metrics with the source. An empty
// signal list enables every metric supported by this sensor. The registered
// set cannot change after metric collection starts.
func New(source *dcgmsource.Source, signalIDs ...string) (collect.Sensor, error) {
	if source == nil {
		return nil, fmt.Errorf("DCGM source is required")
	}

	signalsToWatch, err := resolveSignalsToWatch(signalIDs)
	if err != nil {
		return nil, err
	}
	if err := source.AddMetricsToWatch(signalsToWatch.IDs()); err != nil {
		return nil, fmt.Errorf("configure DCGM metric watch: %w", err)
	}
	return &sensor{source: source, signals: signalsToWatch}, nil
}

// resolveSignalsToWatch validates the configured signals, removes duplicates,
// and preserves their order.
func resolveSignalsToWatch(requested []string) (collect.Signals, error) {
	if len(requested) == 0 {
		return slices.Clone(supportedSignals), nil
	}
	signalsToWatch := make(collect.Signals, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, signalID := range requested {
		if _, duplicate := seen[signalID]; duplicate {
			continue
		}
		definition, supported := supportedByID[signalID]
		if !supported {
			return nil, fmt.Errorf("DCGM sensor does not collect signal %q", signalID)
		}
		seen[signalID] = struct{}{}
		signalsToWatch = append(signalsToWatch, definition)
	}
	return signalsToWatch, nil
}

func (sensor *sensor) ID() string { return ID }

func (sensor *sensor) Signals() collect.Signals {
	return slices.Clone(sensor.signals)
}

// Measure enumerates GPUs once and reads all selected fields for each GPU.
// Device and field failures become observations without hiding successful
// results.
func (sensor *sensor) Measure(
	ctx context.Context,
	options ...collect.OpOption,
) (*observation.ObservationBatch, error) {
	op := collect.NewOp(options...)
	selected, err := op.Signals(sensor.signals)
	if err != nil {
		return nil, fmt.Errorf("select DCGM signals: %w", err)
	}
	if len(selected) == 0 {
		return &observation.ObservationBatch{}, nil
	}

	currentCycle := op.Cycle()
	if currentCycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	startedAt := currentCycle.StartedAt()
	batch := &observation.ObservationBatch{CollectionId: currentCycle.CollectionID()}
	snapshot := sensor.source.Inventory(ctx, currentCycle)
	batch.Inventory = &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{snapshot}}
	failure := snapshot.GetCollectionError()
	if failure != nil {
		sensor.appendErrors(batch, selected, nil, startedAt, failure.Operation, failure)
		return batch, nil
	}
	if len(snapshot.GetInventory().GetEntries()) == 0 {
		sensor.appendNoTargetObservations(batch, selected, startedAt)
		return batch, nil
	}

	for _, entry := range snapshot.GetInventory().GetEntries() {
		target := inventoryEntryTarget(entry)
		if failure := entry.GetCollectionError(); failure != nil {
			sensor.appendErrors(batch, selected, target, startedAt, "inventory", failure)
			continue
		}

		values := sensor.source.ReadMetrics(ctx, currentCycle, uint(entry.GetSourceIndex()), selected.IDs())
		for _, definition := range selected {
			signalID := definition.ID()
			field, exists := values[signalID]
			if !exists {
				sensor.appendError(batch, definition, target, startedAt, "metrics",
					&observation.CollectionError{
						Category: observation.CollectionErrorCategorySourceError,
						Detail:   fmt.Sprintf("DCGM returned no field for signal %q", signalID),
					})
				continue
			}
			sensor.appendField(batch, definition, target, field, startedAt)
		}
	}
	return batch, nil
}

// inventoryEntryTarget uses the DCGM index when UUID collection failed, preserving
// enough identity to report a device-specific collection error.
func inventoryEntryTarget(entry *observation.GPUInventoryEntry) *observation.ObservationTarget {
	if entry.GetDevice().GetId() == "" {
		return observation.GPUSourceLocalTarget(fmt.Sprintf("dcgm-index-%d", entry.GetSourceIndex()))
	}
	return observation.GPUTarget(entry.GetDevice().GetId())
}

func (sensor *sensor) appendNoTargetObservations(
	batch *observation.ObservationBatch,
	definitions collect.Signals,
	observedAt time.Time,
) {
	for _, definition := range definitions {
		batch.Observations = append(batch.Observations, observation.NewNoValueObservation(
			definition.ID(),
			definition.Unit(),
			sensor.source.ID(),
			nil,
			timestamppb.New(observedAt),
			&observation.NoValue{
				Reason: observation.NoValueReasonNoTargets,
				Detail: "No GPUs detected",
			},
		))
	}
}

func (sensor *sensor) appendField(
	batch *observation.ObservationBatch,
	definition collect.Signal,
	target *observation.ObservationTarget,
	field dcgmsource.FieldValue,
	fallbackTime time.Time,
) {
	observedAt := field.ObservedAt
	if observedAt.IsZero() {
		observedAt = fallbackTime
	}
	switch {
	case field.CollectionError != nil:
		sensor.appendError(batch, definition, target, observedAt, "metrics", field.CollectionError)
	case field.NoValue != nil:
		batch.Observations = append(batch.Observations, observation.NewNoValueObservation(
			definition.ID(),
			definition.Unit(),
			sensor.source.ID(),
			target,
			timestamppb.New(observedAt),
			field.NoValue,
		))
	case field.Value == nil || field.Value.Kind == nil:
		sensor.appendError(batch, definition, target, observedAt, "metrics",
			&observation.CollectionError{
				Category: observation.CollectionErrorCategoryParseError,
				Detail:   "DCGM field has no value",
			})
	default:
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId:   definition.ID(),
			Source:     sensor.source.ID(),
			Target:     target,
			ObservedAt: timestamppb.New(observedAt),
			Unit:       definition.Unit(),
			Outcome:    &observation.Observation_Value{Value: field.Value},
		})
	}
}

func (sensor *sensor) appendErrors(
	batch *observation.ObservationBatch,
	definitions collect.Signals,
	target *observation.ObservationTarget,
	observedAt time.Time,
	operation string,
	failure *observation.CollectionError,
) {
	for _, definition := range definitions {
		sensor.appendError(batch, definition, target, observedAt, operation, failure)
	}
}

func (sensor *sensor) appendError(
	batch *observation.ObservationBatch,
	definition collect.Signal,
	target *observation.ObservationTarget,
	observedAt time.Time,
	operation string,
	failure *observation.CollectionError,
) {
	batch.Observations = append(batch.Observations,
		observation.NewCollectionErrorObservation(
			definition.ID(),
			definition.Unit(),
			sensor.source.ID(),
			operation,
			target,
			timestamppb.New(observedAt),
			failure,
		),
	)
}
