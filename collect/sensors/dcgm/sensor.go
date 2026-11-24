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
	signals collect.Signals
}

type gpuSource interface {
	ID() string
	Collect(context.Context, *collect.Cycle, dcgmsource.CollectionRequest) dcgmsource.CollectionResult
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

// Measure reports selected metrics, health, and optional GPU inventory for one
// collection cycle. Source failures become observations alongside collected values.
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
	batch := &observation.ObservationBatch{CollectionId: currentCycle.CollectionID()}
	inventory := op.GPUInventory()
	if len(selected) == 0 && !inventory {
		return batch, nil
	}
	metrics, health := splitSignals(selected)
	result := sensor.source.Collect(ctx, currentCycle, dcgmsource.CollectionRequest{
		Metrics: metrics.IDs(), Health: health, Inventory: inventory,
	})
	startedAt := currentCycle.StartedAt()
	if len(metrics) > 0 {
		sensor.appendMetrics(batch, metrics, startedAt, result.Metrics, result.DiscoveryError)
	}
	if health {
		sensor.appendHealth(batch, startedAt, result.HealthReport, result.HealthError)
	}
	if inventory {
		batch.Inventory = &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{result.Inventory}}
	}
	return batch, nil
}

// appendMetrics formats each GPU's fields without discarding partial values.
func (sensor *sensor) appendMetrics(
	batch *observation.ObservationBatch,
	metrics collect.Signals,
	startedAt time.Time,
	gpuMetrics []dcgmsource.GPUMetrics,
	discoveryError *observation.CollectionError,
) {
	if discoveryError != nil {
		sensor.appendErrors(batch, metrics, nil, startedAt, discoveryError.Operation, discoveryError)
		return
	}
	if len(gpuMetrics) == 0 {
		sensor.appendNoTargetObservations(batch, metrics, startedAt)
		return
	}
	for _, result := range gpuMetrics {
		target := identityEntryTarget(result.Identity)
		for _, definition := range metrics {
			field, exists := result.Fields[definition.ID()]
			if !exists {
				sensor.appendError(batch, definition, target, startedAt, "metrics",
					&observation.CollectionError{
						Category: observation.CollectionErrorCategorySourceError,
						Detail:   fmt.Sprintf("DCGM returned no field for signal %q", definition.ID()),
					})
				continue
			}
			sensor.appendField(batch, definition, target, field, startedAt)
		}
	}
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
