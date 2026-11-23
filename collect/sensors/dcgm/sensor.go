// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/sensorids"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

var _ collect.Sensor = (*sensor)(nil)

// ID identifies the DCGM-backed GPU sensor.
const ID = sensorids.DCGM

// dcgmGPUEntityKind is DCGM's FE_GPU entity kind.
const dcgmGPUEntityKind = 1

// sensor turns configured DCGM metrics and health systems into observations.
type sensor struct {
	source  gpuSource
	health  healthSource
	signals collect.Signals
}

type gpuSource interface {
	ID() string
	GPUIndices(context.Context, *collect.Cycle) ([]uint, *observation.CollectionError)
	GPUUUIDs(context.Context, *collect.Cycle, []uint) []dcgmsource.GPUIdentityEntry
	GPUInventory(context.Context, *collect.Cycle, []dcgmsource.GPUIdentityEntry) *observation.GPUInventorySnapshot
	ReadGPUMetrics(context.Context, *collect.Cycle, uint, []string) map[string]dcgmsource.FieldValue
}

type metricSource interface {
	gpuSource
	AddMetricsToWatch([]string) error
}

// New creates a DCGM sensor and registers its metrics with the source. An empty
// signal list enables every metric supported by this sensor. The registered
// set cannot change after the source's first metric or health read. Inventory
// reads leave registration open. Health collection requires NewWithHealth.
func New(source *dcgmsource.Source, signalIDs ...string) (collect.Sensor, error) {
	if source == nil {
		return nil, fmt.Errorf("DCGM source is required")
	}

	metrics, err := resolveMetricSignals(signalIDs)
	if err != nil {
		return nil, err
	}
	return newSensor(source, metrics)
}

// newSensor registers resolved metrics and creates a metric-only sensor.
func newSensor(source metricSource, metrics collect.Signals) (*sensor, error) {
	if len(metrics) > 0 {
		if err := source.AddMetricsToWatch(metrics.IDs()); err != nil {
			return nil, fmt.Errorf("configure DCGM metric watch: %w", err)
		}
	}
	return &sensor{source: source, signals: metrics}, nil
}

// ID returns the sensor's stable identifier.
func (sensor *sensor) ID() string { return ID }

// Signals returns a copy of the configured output definitions.
func (sensor *sensor) Signals() collect.Signals {
	return slices.Clone(sensor.signals)
}

// Measure collects selected metrics and, when selected, one aggregate health
// report before optional GPU inventory. It requires a collection cycle and
// returns source failures as observations while preserving collected values.
// GPU UUIDs are looked up before metric reads. Missing UUIDs retain native
// identities; a UUID lookup timeout can prevent subsequent native reads.
// WithSignalID() and WithGPUInventory() together request inventory only.
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
	metrics, health := splitSignals(selected)
	var gpuIndices []uint
	var identities []dcgmsource.GPUIdentityEntry
	var enumerationFailure *observation.CollectionError
	if len(metrics) > 0 {
		gpuIndices, enumerationFailure = sensor.source.GPUIndices(ctx, currentCycle)
		if enumerationFailure == nil && len(gpuIndices) > 0 {
			// A later GPU timeout must not prevent identifying earlier samples.
			identities = sensor.source.GPUUUIDs(ctx, currentCycle, gpuIndices)
		}
	}
	if enumerationFailure != nil {
		sensor.appendErrors(batch, metrics, nil, startedAt, enumerationFailure.Operation, enumerationFailure)
	} else if len(metrics) > 0 {
		if len(gpuIndices) == 0 {
			sensor.appendNoTargetObservations(batch, metrics, startedAt)
		}
		for _, entry := range identities {
			target := identityEntryTarget(entry)
			values := sensor.source.ReadGPUMetrics(ctx, currentCycle, entry.Index, metrics.IDs())
			for _, definition := range metrics {
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
	}
	if health {
		sensor.appendHealth(ctx, currentCycle, batch, startedAt)
	}
	// Inventory-only discovery must not prevent a selected health read.
	if op.GPUInventory() && len(metrics) == 0 {
		gpuIndices, enumerationFailure = sensor.source.GPUIndices(ctx, currentCycle)
		if enumerationFailure == nil && len(gpuIndices) > 0 {
			identities = sensor.source.GPUUUIDs(ctx, currentCycle, gpuIndices)
		}
	}
	// Values remain in the batch if inventory enrichment times out.
	if op.GPUInventory() {
		var snapshot *observation.GPUInventorySnapshot
		if enumerationFailure != nil {
			snapshot = &observation.GPUInventorySnapshot{
				Source: sensor.source.ID(), CollectedAt: timestamppb.Now(),
				Outcome: &observation.GPUInventorySnapshot_CollectionError{CollectionError: enumerationFailure},
			}
		} else {
			snapshot = sensor.source.GPUInventory(ctx, currentCycle, identities)
		}
		batch.Inventory = &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{snapshot}}
	}
	return batch, nil
}

// identityEntryTarget retains the native GPU ID and optional UUID and PCI address.
// Missing UUIDs retain the existing source-local fallback for compatibility.
func identityEntryTarget(entry dcgmsource.GPUIdentityEntry) *observation.ObservationTarget {
	gpu := &observation.GPURef{
		Uuid:  entry.UUID,
		BusId: entry.BusID,
		Native: &observation.NativeEntityRef{
			Source: dcgmsource.Name, EntityKind: dcgmGPUEntityKind, EntityId: uint32(entry.Index),
		},
	}
	if entry.UUID == "" {
		gpu.SourceLocalId = fmt.Sprintf("dcgm-index-%d", entry.Index)
	}
	return &observation.ObservationTarget{Kind: &observation.ObservationTarget_GPU{Gpu: gpu}}
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
