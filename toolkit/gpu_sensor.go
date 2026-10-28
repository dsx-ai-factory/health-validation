// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// GPUSensor collects GPU health measurements via a pluggable backend.
// It implements [Sensor].
type GPUSensor struct {
	backend GPUBackend
}

// NewGPUSensor creates a GPU sensor backed by the given backend.
//
// For production:
//
//	sensor := toolkit.NewGPUSensor(backend)
//
// For testing:
//
//	sensor := toolkit.NewGPUSensor(toolkit.MockGPU(
//	    gpu.GPUMeasurement{GPUID: "GPU-0"},
//	))
func NewGPUSensor(backend GPUBackend) *GPUSensor {
	return &GPUSensor{backend: backend}
}

func (s *GPUSensor) Name() string { return "gpu" }

// Measure does not use the context: the GPU backend reads NVML over cgo,
// which cannot be interrupted mid-call, so collection is bounded by the
// caller's watchdog rather than the context (see [Sensor.Measure]).
func (s *GPUSensor) Measure(_ context.Context) ([]health.Measurement, error) {
	return s.backend.MeasureGPUs()
}

// Errored reports the whole GPU domain as unobservable — one errored
// measurement covering every GPU signal — so per-GPU checks fail
// Critical when collection could not run.
func (s *GPUSensor) Errored(err error) []health.Measurement {
	m := gpu.ErroredGPU(s.Name(), err, gpu.MeasurementSignals()...)
	m.SetEntityKey(health.SourceEntity(sensorSource(s.Name())))
	return []health.Measurement{m}
}

// ResourceKey groups this sensor with the others sharing its backend (the
// shared multi-domain source feeds the GPU, Node, and NVLink sensors) so
// Collect's circuit breaker skips the rest once that backend times out.
func (s *GPUSensor) ResourceKey() string { return backendResourceKey(s.backend) }
