// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband exposes sysfs InfiniBand readings as observations.
package infiniband

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
)

const ID = "infiniband.sysfs"

type sensor struct {
	source   *ibsource.Source
	ethernet bool
}

// Option configures a sensor returned by [New].
type Option func(*sensor)

// WithEthernetPorts also reports ports whose link layer is Ethernet: RoCE
// ports on the same mlx5 HCAs. GPU east-west fabrics built on RoCE expose
// their rails only this way, so a check of fabric port state needs them.
// Every port reading still carries the port's link layer.
func WithEthernetPorts() Option {
	return func(s *sensor) { s.ethernet = true }
}

// New returns a sensor using the supplied source, or the host sysfs when nil.
// Like GPUd, it excludes ports whose link layer is not InfiniBand, unless
// [WithEthernetPorts] also admits Ethernet (RoCE) ports.
func New(source *ibsource.Source, opts ...Option) collect.Sensor {
	if source == nil {
		source = ibsource.New(nil)
	}
	s := &sensor{source: source}
	for _, opt := range opts {
		opt(s)
	}
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
		if reading.LinkLayer != nil && !s.admits(*reading.LinkLayer) {
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

// admits reports whether a port with linkLayer is reported.
func (s *sensor) admits(linkLayer string) bool {
	return strings.EqualFold(linkLayer, "infiniband") || (s.ethernet && strings.EqualFold(linkLayer, "ethernet"))
}
