// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/sensorids"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

type recoveringDCGMSensor struct {
	open         func() (collect.Sensor, func() error, func() bool, error)
	sensor       collect.Sensor
	closeSession func() error
	sourceClosed func() bool
	signals      collect.Signals
	mu           sync.Mutex
	disconnected bool
}

func (*recoveringDCGMSensor) ID() string                 { return sensorids.DCGM }
func (s *recoveringDCGMSensor) Signals() collect.Signals { return slices.Clone(s.signals) }

func (s *recoveringDCGMSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	if !s.mu.TryLock() {
		return nil, dcgmsource.ErrInUse
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.disconnected || (s.sourceClosed != nil && s.sourceClosed()) {
		if err := s.closeLocked(); err != nil {
			if errors.Is(err, dcgmsource.ErrInUse) {
				return nil, err
			}
			slog.Warn("DCGM session cleanup before reopening failed", "error", err)
		}
	}
	if s.sensor == nil {
		sensor, cleanup, closed, err := s.open()
		if err != nil {
			return nil, err
		}
		s.sensor, s.closeSession, s.sourceClosed = sensor, cleanup, closed
	}
	batch, err := s.sensor.Measure(ctx, opts...)
	for _, current := range batch.GetObservations() {
		if current.GetCollectionError().GetSourceError() == "DCGM_ST_CONNECTION_NOT_VALID" {
			s.disconnected = true
			break
		}
	}
	return batch, err
}

func (s *recoveringDCGMSensor) Close() error {
	if !s.mu.TryLock() {
		return dcgmsource.ErrInUse
	}
	defer s.mu.Unlock()
	return s.closeLocked()
}

// Keep a busy session until its native calls and final cleanup have finished.
func (s *recoveringDCGMSensor) closeLocked() error {
	if s.closeSession == nil {
		return nil
	}
	err := s.closeSession()
	if errors.Is(err, dcgmsource.ErrInUse) {
		return err
	}
	s.sensor, s.closeSession, s.sourceClosed, s.disconnected = nil, nil, nil, false
	return err
}
