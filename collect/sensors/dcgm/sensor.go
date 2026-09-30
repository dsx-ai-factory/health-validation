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
	source  gpuSource
	signals collect.Signals
}

type gpuSource interface {
	ID() string
	GPUIdentities(context.Context, *collect.Cycle) ([]dcgmsource.GPUIdentityEntry, *observation.CollectionError)
	GPUInventory(context.Context, *collect.Cycle) *observation.GPUInventorySnapshot
	ReadGPUMetrics(context.Context, *collect.Cycle, uint, []string) map[string]dcgmsource.FieldValue
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
// WithSignalID() and WithGPUInventory() together request inventory only.
// GPU and field failures become observations without hiding successful
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
	currentCycle := op.Cycle()
	if currentCycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	startedAt := currentCycle.StartedAt()
	batch := &observation.ObservationBatch{CollectionId: currentCycle.CollectionID()}
	if len(selected) == 0 && !op.GPUInventory() {
		return batch, nil
	}
	var identities []dcgmsource.GPUIdentityEntry
	var failure *observation.CollectionError
	if op.GPUInventory() {
		snapshot := sensor.source.GPUInventory(ctx, currentCycle)
		batch.Inventory = &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{snapshot}}
		identities, failure = identitiesFromInventory(snapshot)
	} else {
		identities, failure = sensor.source.GPUIdentities(ctx, currentCycle)
	}
	if len(selected) == 0 {
		return batch, nil
	}
	if failure != nil {
		sensor.appendErrors(batch, selected, nil, startedAt, failure.Operation, failure)
		return batch, nil
	}
	if len(identities) == 0 {
		sensor.appendNoTargetObservations(batch, selected, startedAt)
		return batch, nil
	}

	for _, entry := range identities {
		target := identityEntryTarget(entry)
		if failure := entry.CollectionError; failure != nil {
			sensor.appendErrors(batch, selected, target, startedAt, failure.Operation, failure)
			continue
		}

		values := sensor.source.ReadGPUMetrics(ctx, currentCycle, entry.Index, selected.IDs())
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

func identitiesFromInventory(snapshot *observation.GPUInventorySnapshot) ([]dcgmsource.GPUIdentityEntry, *observation.CollectionError) {
	identities := make([]dcgmsource.GPUIdentityEntry, 0, len(snapshot.GetInventory().GetEntries()))
	for _, entry := range snapshot.GetInventory().GetEntries() {
		identities = append(identities, dcgmsource.GPUIdentityEntry{
			Index:           uint(entry.GetSourceIndex()),
			UUID:            entry.GetGpu().GetUuid(),
			CollectionError: entry.GetCollectionError(),
		})
	}
	return identities, snapshot.GetCollectionError()
}

// identityEntryTarget uses the DCGM index when UUID collection failed, preserving
// enough identity to report a device-specific collection error.
func identityEntryTarget(entry dcgmsource.GPUIdentityEntry) *observation.ObservationTarget {
	if entry.UUID == "" {
		return observation.GPUSourceLocalTarget(fmt.Sprintf("dcgm-index-%d", entry.Index))
	}
	return observation.GPUTarget(entry.UUID)
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
