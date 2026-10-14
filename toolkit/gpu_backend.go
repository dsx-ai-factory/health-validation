// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package toolkit provides sensor implementations for collecting
// hardware measurements that feed into the health check catalog.
package toolkit

import (
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// GPUBackend supplies the per-GPU measurements consumed by the GPU sensor:
// one [gpu.GPUMeasurement] per GPU enumerated by the data source, wrapped in
// [health.Measurement]. GPUs whose handle could not be loaded are surfaced
// via [gpu.ErroredGPU] so the catalog can fail per-GPU checks for those slots.
//
// It is one of the three narrow, per-domain backend interfaces ([NodeBackend],
// [NVLinkBackend]); a full multi-domain source (e.g. the nvml backend)
// implements all three. The method names differ across the three, so the
// interfaces are genuinely distinct: a NodeBackend does not satisfy GPUBackend.
type GPUBackend interface {
	MeasureGPUs() ([]health.Measurement, error)
}

// backendResourceKey identifies a backend instance so the sensors wrapping
// the same backend (the GPU, Node, and NVLink sensors share the one host
// source) report the same key. Collect's circuit breaker uses it to skip
// the rest once that backend's collection times out rather than re-entering
// a wedged data source per sensor — the guarantee that stops a second sensor
// from concurrently re-entering an unsynchronized backend while a timed-out
// goroutine is still parked inside it.
//
// It relies on distinct backend instances having distinct addresses, which
// holds for the pointer-typed sources here (the lazy NVML wrapper). A nil
// backend is unkeyed. Value-type mock backends never time out, so their key
// is irrelevant.
func backendResourceKey(b any) string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("%p", b)
}

// MockGPU returns a [GPUBackend] that emits the provided per-GPU measurements.
//
//	sensor := toolkit.NewGPUSensor(toolkit.MockGPU(
//	    gpu.GPUMeasurement{GPUID: "GPU-0"},
//	    gpu.GPUMeasurement{GPUID: "GPU-7"},
//	))
func MockGPU(gpus ...gpu.GPUMeasurement) GPUBackend {
	out := make([]health.Measurement, len(gpus))
	for i, g := range gpus {
		out[i] = g
	}
	return mockGPUBackend{measurements: out}
}

type mockGPUBackend struct {
	measurements []health.Measurement
}

func (m mockGPUBackend) MeasureGPUs() ([]health.Measurement, error) {
	return m.measurements, nil
}
