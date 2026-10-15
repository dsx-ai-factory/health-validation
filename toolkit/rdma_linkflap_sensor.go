// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// RDMALinkFlapSensor collects mlx5 RDMA link-flap events via a pluggable
// backend. It implements [Sensor].
type RDMALinkFlapSensor struct {
	backend RDMALinkFlapBackend
}

// NewRDMALinkFlapSensor creates a link-flap sensor backed by the given
// backend.
//
// For production:
//
//	sensor := toolkit.NewRDMALinkFlapSensor(toolkit.JournalctlRDMALinkFlap())
//
// For testing, back it with [MockRDMALinkFlap].
func NewRDMALinkFlapSensor(backend RDMALinkFlapBackend) *RDMALinkFlapSensor {
	return &RDMALinkFlapSensor{backend: backend}
}

func (s *RDMALinkFlapSensor) Name() string { return "rdma_linkflap" }

// Measure forwards the context to the backend: the journal read is a
// cancellable command, so it honors the deadline (see [Sensor.Measure]).
func (s *RDMALinkFlapSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	return s.backend.Measure(ctx)
}

// Errored reports the journal as unreadable so the link-flap check fails
// Critical when collection could not run.
func (s *RDMALinkFlapSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{infiniband.ErroredLinkFlap(sinceBoot, err, infiniband.MeasurementSignals...)}
}
