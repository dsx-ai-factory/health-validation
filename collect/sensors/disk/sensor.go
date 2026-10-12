// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package disk exposes read-only block and filesystem observations.
package disk

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/disk"
	source "github.com/dsx-ai-factory/health-validation/collect/source/disk"
)

const ID = source.Name

type sensor struct{ source *source.Source }

func New(files fs.FS, opts ...source.Option) collect.Sensor {
	return NewWithSource(source.New(files, opts...))
}

func NewWithSource(src *source.Source) collect.Sensor {
	if src == nil {
		src = source.New(nil)
	}
	return &sensor{source: src}
}
func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return signals.All() }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	op := collect.NewOp(opts...)
	if op.GPUInventory() {
		return nil, fmt.Errorf("GPU inventory is not supported by %s", ID)
	}
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	batch := &observation.ObservationBatch{}
	cycle := op.Cycle()
	if cycle != nil {
		batch.CollectionId = cycle.CollectionID()
	}
	if len(selected) == 0 {
		return batch, nil
	}
	startedAt := time.Now()
	if cycle != nil {
		startedAt = cycle.StartedAt()
	} else {
		cycle, err = collect.NewCycle("disk.local", startedAt)
		if err != nil {
			return nil, err
		}
	}
	now := timestamppb.New(startedAt)
	readings, err := collect.Run(ctx, cycle, ID, func(ctx context.Context) ([]source.Reading, error) { return s.source.Read(ctx, selected) })
	if err != nil {
		for _, signal := range selected {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(), ID, "read", nil, now, source.Failure(err)))
		}
		return batch, nil
	}
	for _, reading := range readings {
		if reading.Failure != nil {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(reading.Signal.ID(), reading.Signal.Unit(), ID, "read", reading.Target, now, reading.Failure))
			continue
		}
		if reading.NoValue != nil {
			batch.Observations = append(batch.Observations, observation.NewNoValueObservation(reading.Signal.ID(), reading.Signal.Unit(), ID, reading.Target, now, reading.NoValue))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{SignalId: reading.Signal.ID(), Unit: reading.Signal.Unit(), Source: ID, Target: reading.Target, ObservedAt: now, Outcome: &observation.Observation_Value{Value: reading.Value}})
	}
	return batch, nil
}
