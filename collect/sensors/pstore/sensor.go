// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pstore exposes persistent kernel records without assigning crash times.
package pstore

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pstoresignals "github.com/dsx-ai-factory/health-validation/collect/signals/pstore"
	pstoresource "github.com/dsx-ai-factory/health-validation/collect/source/pstore"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const ID = pstoresource.Name

type sensor struct{ source *pstoresource.Source }

func New(files fs.FS) collect.Sensor { return NewWithRoots(files) }

// NewWithRoots accepts directory paths relative to files. Nil files uses /.
func NewWithRoots(files fs.FS, roots ...string) collect.Sensor {
	return &sensor{source: pstoresource.New(files, roots...)}
}
func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return pstoresignals.All() }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collection context is required")
	}
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	if op.GPUInventory() {
		return nil, fmt.Errorf("pstore sensor does not provide GPU inventory")
	}
	cycle := op.Cycle()
	batch := &observation.ObservationBatch{CollectionId: cycle.CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	now := time.Now().UTC()
	if cycle != nil {
		now = cycle.StartedAt()
	}
	observedAt := timestamppb.New(now)
	var readings []pstoresource.Reading
	if cycle == nil {
		readings, err = s.source.Read(ctx, selected)
	} else {
		readings, err = collect.Run(ctx, cycle, ID, func(ctx context.Context) ([]pstoresource.Reading, error) { return s.source.Read(ctx, selected) })
	}
	if err != nil {
		for _, signal := range selected {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(), ID, "scan", observation.NodeTarget(""), observedAt, pstoresource.Failure(err)))
		}
		return batch, nil
	}
	for _, reading := range readings {
		if reading.Failure != nil {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(reading.Signal.ID(), reading.Signal.Unit(), ID, reading.Path, reading.Target, observedAt, reading.Failure))
			continue
		}
		if reading.NoValue != nil {
			batch.Observations = append(batch.Observations, observation.NewNoValueObservation(reading.Signal.ID(), reading.Signal.Unit(), ID, reading.Target, observedAt, reading.NoValue))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{SignalId: reading.Signal.ID(), Source: ID, Target: reading.Target, ObservedAt: observedAt, Unit: reading.Signal.Unit(), Outcome: &observation.Observation_Value{Value: reading.Value}})
	}
	return batch, nil
}
