// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband exposes sysfs InfiniBand readings as observations.
package infiniband

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
)

const ID = "infiniband.sysfs"

type sensor struct{ source *ibsource.Source }

// New returns a sensor using the supplied source, or the host sysfs when nil.
func New(source *ibsource.Source) collect.Sensor {
	if source == nil {
		source = ibsource.New(nil)
	}
	return &sensor{source: source}
}

func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return ibsignals.All() }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}, nil
	}
	cycle := op.Cycle()
	if cycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	now := timestamppb.New(cycle.StartedAt())
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	readings, err := collect.Run(ctx, cycle, ibsource.Name, func(ctx context.Context) ([]ibsource.Reading, error) { return s.source.Read(ctx, selected) })
	if err != nil {
		for _, signal := range selected {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(), ibsource.Name, "read", nil, now, ibsource.Failure(err)))
		}
		return batch, nil
	}
	for _, reading := range readings {
		entity := reading.Entity
		if reading.Failure != nil {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(reading.Signal.ID(), reading.Signal.Unit(), ibsource.Name, "read", entity, now, reading.Failure))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId:   reading.Signal.ID(),
			Unit:       reading.Signal.Unit(),
			Source:     ibsource.Name,
			Entity:     entity,
			ObservedAt: now,
			Outcome:    &observation.Observation_Value{Value: reading.Value},
		})
	}
	return batch, nil
}
