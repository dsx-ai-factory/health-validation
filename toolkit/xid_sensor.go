// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// XidSensor collects NVRM Xid events via a pluggable backend. It
// implements [Sensor].
type XidSensor struct {
	backend XidBackend
}

// NewXidSensor creates an Xid sensor backed by the given backend.
//
// For production:
//
//	sensor := toolkit.NewXidSensor(toolkit.Journalctl())
//
// For testing:
//
//	sensor := toolkit.NewXidSensor(toolkit.MockXid(
//	    "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
//	))
func NewXidSensor(backend XidBackend) *XidSensor {
	return &XidSensor{backend: backend}
}

func (s *XidSensor) Name() string { return "xid" }

// Measure forwards the context to the backend: the journal read is a
// cancellable command, so it honors the deadline (see [Sensor.Measure]).
func (s *XidSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	return s.backend.Measure(ctx)
}

// Errored reports the Xid journal as unreadable so Xid checks fail
// Critical when collection could not run.
func (s *XidSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{xid.ErroredXid(sinceBoot, err, xid.MeasurementSignals()...)}
}
