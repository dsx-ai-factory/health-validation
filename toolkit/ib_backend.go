// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// IBBackend supplies the measurements consumed by the generic InfiniBand sensor.
//
// The catalog's only built-in InfiniBand check (rdma_linkflap_fatal) is fed by
// the dedicated journal link-flap sensor, not this backend. This sensor is
// opt-in: a consumer supplies a backend to contribute additional InfiniBand
// measurements, owning their device enumeration, the measurement types it
// emits, and — via Errored — the errored measurements those checks see when
// collection fails.
type IBBackend interface {
	MeasureIB(ctx context.Context) ([]health.Measurement, error)

	// Errored returns the measurements to record when collection could not
	// run (a timeout or a Measure error). The backend owns them because it
	// owns the measurement types; returning an errored measurement here is
	// what fails the dependent checks instead of silently skipping them.
	Errored(err error) []health.Measurement
}

// MockIB returns an IB backend that emits the provided measurements and no
// errored measurements.
func MockIB(measurements ...health.Measurement) IBBackend {
	return mockIBBackend{measurements: measurements}
}

type mockIBBackend struct {
	measurements []health.Measurement
}

func (m mockIBBackend) MeasureIB(context.Context) ([]health.Measurement, error) {
	return m.measurements, nil
}

func (mockIBBackend) Errored(error) []health.Measurement { return nil }
