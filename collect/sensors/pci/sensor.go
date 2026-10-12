// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pci exposes read-only PCI inventory and raw ACS registers.
package pci

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
	pcisource "github.com/dsx-ai-factory/health-validation/collect/source/pci"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const ID = pcisource.Name

type sensor struct{ source *pcisource.Source }

func New(files fs.FS) collect.Sensor     { return &sensor{source: pcisource.New(files)} }
func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return pcisignals.All() }

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
		return nil, fmt.Errorf("PCI sensor does not provide NVML GPU inventory")
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
	var readings []pcisource.Reading
	if cycle == nil {
		readings, err = s.source.Read(ctx, selected)
	} else {
		readings, err = collect.Run(ctx, cycle, ID, func(ctx context.Context) ([]pcisource.Reading, error) { return s.source.Read(ctx, selected) })
	}
	if err != nil {
		for _, signal := range selected {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(signal.ID(), signal.Unit(), ID, pcisource.Root, observation.NodeTarget(""), observedAt, pcisource.Failure(err)))
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
