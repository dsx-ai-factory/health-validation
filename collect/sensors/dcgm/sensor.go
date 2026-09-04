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
	devices, failure := sensor.source.Devices(ctx, currentCycle)
	if failure != nil {
		sensor.appendErrors(batch, selected, nil, startedAt, "devices", failure)
		return batch, nil
	}

	for _, device := range devices {
		entity := deviceEntity(device)
		if device.CollectionError != nil {
			sensor.appendErrors(batch, selected, entity, startedAt, "device_info", device.CollectionError)
			continue
		}

		values := sensor.source.ReadMetrics(ctx, currentCycle, device, selected.IDs())
		for _, definition := range selected {
			signalID := definition.ID()
			field, exists := values[signalID]
			if !exists {
				sensor.appendError(batch, definition, entity, startedAt, "metrics",
					&observation.CollectionError{
						Category: observation.CollectionErrorCategorySourceError,
						Detail:   fmt.Sprintf("DCGM returned no field for signal %q", signalID),
					})
				continue
			}
			sensor.appendField(batch, definition, entity, field, startedAt)
		}
	}
	return batch, nil
}

// deviceEntity uses the DCGM index when UUID collection failed, preserving
// enough identity to report a device-specific collection error.
func deviceEntity(device dcgmsource.Device) *observation.Entity {
	id := device.UUID
	if id == "" {
		id = fmt.Sprintf("dcgm-index-%d", device.Index)
	}
	return observation.GPUEntity(id)
}

func (sensor *sensor) appendField(
	batch *observation.ObservationBatch,
	definition collect.Signal,
	entity *observation.Entity,
	field dcgmsource.FieldValue,
	fallbackTime time.Time,
) {
	observedAt := field.ObservedAt
	if observedAt.IsZero() {
		observedAt = fallbackTime
	}
	switch {
	case field.CollectionError != nil:
		sensor.appendError(batch, definition, entity, observedAt, "metrics", field.CollectionError)
	case field.Value == nil || field.Value.Kind == nil:
		sensor.appendError(batch, definition, entity, observedAt, "metrics",
			&observation.CollectionError{
				Category: observation.CollectionErrorCategoryParseError,
				Detail:   "DCGM field has no value",
			})
	default:
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId:   definition.ID(),
			Source:     sensor.source.ID(),
			Entity:     entity,
			ObservedAt: timestamppb.New(observedAt),
			Unit:       definition.Unit(),
			Outcome:    &observation.Observation_Value{Value: field.Value},
		})
	}
}

func (sensor *sensor) appendErrors(
	batch *observation.ObservationBatch,
	definitions collect.Signals,
	entity *observation.Entity,
	observedAt time.Time,
	operation string,
	failure *observation.CollectionError,
) {
	for _, definition := range definitions {
		sensor.appendError(batch, definition, entity, observedAt, operation, failure)
	}
}

func (sensor *sensor) appendError(
	batch *observation.ObservationBatch,
	definition collect.Signal,
	entity *observation.Entity,
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
			entity,
			timestamppb.New(observedAt),
			failure,
		),
	)
}
