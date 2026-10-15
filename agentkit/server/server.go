// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package server runs one collection cycle per request over the registered
// sensors and serves the combined batch over gRPC and HTTP on one TCP port —
// the prototype's single-port surface.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
)

type Server struct {
	sensors []collect.Sensor
}

func New() *Server {
	return &Server{}
}

// Register adds sensors before the server starts handling requests.
func (s *Server) Register(registered ...collect.Sensor) {
	s.sensors = append(s.sensors, registered...)
}

// Check runs one collection cycle and returns the combined batch. A failed
// sensor degrades to per-signal collection-error observations built from its
// declared signal IDs; observations already collected this cycle are kept.
func (s *Server) Check(ctx context.Context) (*observation.ObservationBatch, error) {
	collectionID := uuid.NewString()
	currentCycle, err := collect.NewCycle(collectionID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	batch := &observation.ObservationBatch{CollectionId: collectionID}
	for _, sensor := range s.sensors {
		opts := []collect.OpOption{collect.WithCycle(currentCycle)}
		if sensor.ID() == nvmlsensor.ID || sensor.ID() == dcgmsensor.ID {
			opts = append(opts, collect.WithGPUInventory())
		}
		b, err := sensor.Measure(ctx, opts...)
		if err != nil {
			// Source-specific failures should already be observations. Attribute
			// an unexpected top-level failure to the sensor that returned it.
			slog.Warn("sensor failed", "sensor", sensor.ID(), "error", err)
			for _, definition := range sensor.Signals() {
				batch.Observations = append(batch.Observations, observation.NewErrorObservation(definition.ID(), definition.Unit(), sensor.ID(), "Measure", err))
			}
			continue
		}
		if err := observation.ValidateBatch(b); err != nil {
			return nil, fmt.Errorf("sensor %q returned invalid observations: %w", sensor.ID(), err)
		}
		slog.Debug("sensor returned", "sensor", sensor.ID(), "observations", len(b.GetObservations()))
		batch.Observations = append(batch.Observations, b.GetObservations()...)
		if inventory := b.GetInventory(); inventory != nil {
			if batch.Inventory == nil {
				batch.Inventory = &observation.HardwareInventory{}
			}
			batch.Inventory.GpuSnapshots = append(batch.Inventory.GpuSnapshots, inventory.GetGpuSnapshots()...)
		}
	}
	return batch, nil
}
