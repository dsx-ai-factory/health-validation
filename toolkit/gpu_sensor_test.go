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

package toolkit_test

import (
	"context"
	"testing"

	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/toolkit"
)

func TestGPUSensor_MockSingleGPU(t *testing.T) {
	sensor := toolkit.NewGPUSensor(toolkit.MockGPU(
		gpu.GPUMeasurement{GPUID: "GPU-0"},
	))

	if sensor.Name() != "gpu" {
		t.Errorf("name = %q, want %q", sensor.Name(), "gpu")
	}

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1", len(measurements))
	}
	gm, ok := measurements[0].(gpu.GPUMeasurement)
	if !ok {
		t.Fatal("measurement is not gpu.GPUMeasurement")
	}
	if gm.GPUID != "GPU-0" {
		t.Errorf("gpu_id = %v, want GPU-0", gm.GPUID)
	}
}

func TestGPUSensor_MockMultiGPU(t *testing.T) {
	sensor := toolkit.NewGPUSensor(toolkit.MockGPU(
		gpu.GPUMeasurement{GPUID: "GPU-0"},
		gpu.GPUMeasurement{GPUID: "GPU-1"},
		gpu.GPUMeasurement{GPUID: "GPU-2"},
		gpu.GPUMeasurement{GPUID: "GPU-3"},
	))

	measurements, err := sensor.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(measurements) != 4 {
		t.Fatalf("got %d measurements, want 4", len(measurements))
	}
	for i, m := range measurements {
		gm := m.(gpu.GPUMeasurement)
		expected := "GPU-" + string(rune('0'+i))
		if gm.GPUID != expected {
			t.Errorf("measurement %d: gpu_id = %v, want %v", i, gm.GPUID, expected)
		}
	}
}

func TestGPUSensor_NvidiaSMIStub(t *testing.T) {
	sensor := toolkit.NewGPUSensor(toolkit.NvidiaSMI())
	_, err := sensor.Measure(context.Background())
	if err == nil {
		t.Error("expected error from unimplemented nvidia-smi backend")
	}
}
