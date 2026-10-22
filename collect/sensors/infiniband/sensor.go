// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband exposes sysfs InfiniBand readings as observations.
package infiniband

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
)

const ID = "infiniband.sysfs"

type sensor struct {
	source *ibsource.Source
	op     Op
}

// Op holds the sensor options.
type Op struct {
	linkLayers []string
}

// OpOption configures the sensor.
type OpOption func(*Op)

func (op *Op) applyOpts(opts []OpOption) {
	for _, opt := range opts {
		opt(op)
	}
	if len(op.linkLayers) == 0 {
		op.linkLayers = []string{"InfiniBand"}
	}
}

// WithLinkLayers selects the port link layers to report, compared
// case-insensitively against sysfs link_layer ("InfiniBand", "Ethernet").
// The default is InfiniBand only, matching GPUd.
func WithLinkLayers(layers ...string) OpOption {
	return func(op *Op) {
		op.linkLayers = append(op.linkLayers, layers...)
	}
}

// New returns a sensor using the supplied source, or the host sysfs when nil.
// Like GPUd, it excludes ports whose link layer is not InfiniBand unless
// [WithLinkLayers] admits more.
func New(source *ibsource.Source, opts ...OpOption) collect.Sensor {
	if source == nil {
		source = ibsource.New(nil)
	}
	s := &sensor{source: source}
	s.op.applyOpts(opts)
	return s
}

func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return ibsignals.All() }

func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, err
	}
	cycle := op.Cycle()
	if cycle == nil {
		return nil, fmt.Errorf("collection cycle is required")
	}
	if len(selected) == 0 {
		return &observation.ObservationBatch{CollectionId: cycle.CollectionID()}, nil
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
		// GPUd components/accelerator/nvidia/infiniband/types.IBPort.IsIBPort.
		// Preserve failures when link_layer itself could not be read.
		if reading.LinkLayer != nil && !s.reports(*reading.LinkLayer) {
			continue
		}
		if reading.Failure != nil {
			batch.Observations = append(batch.Observations, observation.NewCollectionErrorObservation(reading.Signal.ID(), reading.Signal.Unit(), ibsource.Name, "read", reading.Target, now, reading.Failure))
			continue
		}
		if reading.NoValue != nil {
			batch.Observations = append(batch.Observations, observation.NewNoValueObservation(reading.Signal.ID(), reading.Signal.Unit(), ibsource.Name, reading.Target, now, reading.NoValue))
			continue
		}
		batch.Observations = append(batch.Observations, &observation.Observation{
			SignalId:   reading.Signal.ID(),
			Unit:       reading.Signal.Unit(),
			Source:     ibsource.Name,
			Target:     reading.Target,
			ObservedAt: now,
			Outcome:    &observation.Observation_Value{Value: reading.Value},
		})
	}
	return batch, nil
}

// reports tells whether ports of linkLayer are selected.
func (s *sensor) reports(linkLayer string) bool {
	return slices.ContainsFunc(s.op.linkLayers, func(layer string) bool { return strings.EqualFold(layer, linkLayer) })
}
