// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Sensor collects measurements from a hardware domain.
//
// Each call to Measure produces one measurement per observed entity
// (e.g., one GPUMeasurement per GPU).
type Sensor interface {
	// Name identifies this sensor (e.g., "gpu", "nvlink").
	Name() string

	// Measure collects current state and returns one measurement per
	// observed entity. The context bounds collection: a sensor backed by
	// a cancellable source (a command, a journal read) should honor it,
	// while a source that cannot be interrupted mid-call (NVML over cgo)
	// may ignore it and rely on the caller's watchdog. Returns an error
	// when the data source is unavailable; callers degrade that via
	// Errored rather than dropping the host from the verdict.
	Measure(ctx context.Context) ([]health.Measurement, error)

	// Errored returns stand-in measurements for when this sensor could
	// not collect at all — a hard backend error or a collection timeout.
	// Each names every signal in the sensor's domain as failed (via the
	// domain's ErroredXxx constructor) so the catalog fails exactly the
	// checks that depend on this sensor Critical, while checks fed by
	// other sensors still evaluate. err is the cause and is surfaced in
	// the resulting verdict's message.
	Errored(err error) []health.Measurement
}

// sensorSource names a toolkit sensor as the source of a whole-domain failure,
// so its key cannot collide with another provider's failure of that domain.
func sensorSource(name string) string { return "toolkit." + name }
