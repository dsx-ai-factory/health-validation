/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package toolkit provides sensor implementations for collecting
// hardware measurements that feed into the health check catalog.
package toolkit

import (
	"errors"
	"fmt"

	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
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
// holds for the pointer-typed sources here (the lazy NVML wrapper, the
// nvidia-smi backend). A nil backend is unkeyed. Value-type mock backends
// never time out, so their key is irrelevant.
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

// NvidiaSMI returns an [NvidiaSMIBackend] that collects measurements by
// running nvidia-smi and parsing its output. Like [NVMLBackend] it is a full
// multi-domain source — a concrete type implementing all three Measure
// methods — so it satisfies [GPUBackend], [NodeBackend], and [NVLinkBackend].
func NvidiaSMI() *NvidiaSMIBackend {
	return &NvidiaSMIBackend{}
}

// NvidiaSMIBackend is a full multi-domain source backed by the nvidia-smi
// CLI. Its collection is not yet implemented.
type NvidiaSMIBackend struct{}

func (n *NvidiaSMIBackend) MeasureGPUs() ([]health.Measurement, error) {
	// TODO: run nvidia-smi -q -x, parse XML, emit one GPUMeasurement per GPU.
	return nil, errors.New("nvidia-smi gpu backend not yet implemented")
}

func (n *NvidiaSMIBackend) MeasureNode() ([]health.Measurement, error) {
	// TODO: run nvidia-smi -q -x, parse XML, emit one NodeMeasurement
	// with GPUCount.
	return nil, errors.New("nvidia-smi node backend not yet implemented")
}

func (n *NvidiaSMIBackend) MeasureNVLink() ([]health.Measurement, error) {
	// TODO: run nvidia-smi -q -x, parse fabric health, emit one
	// NVLinkMeasurement.
	return nil, errors.New("nvidia-smi nvlink backend not yet implemented")
}
