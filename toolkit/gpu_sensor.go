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

package toolkit

import (
	"context"

	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
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
//	sensor := toolkit.NewGPUSensor(toolkit.NvidiaSMI())
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
	return []health.Measurement{gpu.ErroredGPU(s.Name(), err, gpu.MeasurementSignals...)}
}

// ResourceKey groups this sensor with the others sharing its backend (the
// shared multi-domain source feeds the GPU, Node, and NVLink sensors) so
// Collect's circuit breaker skips the rest once that backend times out.
func (s *GPUSensor) ResourceKey() string { return backendResourceKey(s.backend) }
