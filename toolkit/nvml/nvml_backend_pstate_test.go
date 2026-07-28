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

//go:build linux && !android

package nvml_test

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/NVIDIA/health-validation/catalog/gpu"
	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/toolkit/nvml"
)

// pstateLib returns a mock.Interface enumerating one device per entry,
// each reporting its UUID and the given GetPerformanceState result.
func pstateLib(devs ...*mock.Device) gonvml.Interface {
	return &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return len(devs), gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(i int) (gonvml.Device, gonvml.Return) {
			return devs[i], gonvml.SUCCESS
		},
	}
}

func pstateDevice(uuid string, state gonvml.Pstates, ret gonvml.Return) *mock.Device {
	return &mock.Device{
		GetUUIDFunc:             func() (string, gonvml.Return) { return uuid, gonvml.SUCCESS },
		GetPerformanceStateFunc: func() (gonvml.Pstates, gonvml.Return) { return state, ret },
	}
}

// expectGPUMeasurement asserts ms[i] is a GPUMeasurement and returns it.
func expectGPUMeasurement(t *testing.T, ms []health.Measurement, i int) gpu.GPUMeasurement {
	t.Helper()
	m, ok := ms[i].(gpu.GPUMeasurement)
	if !ok {
		t.Fatalf("ms[%d] = %T, want gpu.GPUMeasurement", i, ms[i])
	}
	return m
}

// The built-in GPU probe records each device's performance state as the
// numeric level, so a device below P0 is distinguishable from the P0
// zero value.
func TestMeasureGPUs_Pstate(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(
		pstateDevice("GPU-0", gonvml.PSTATE_0, gonvml.SUCCESS),
		pstateDevice("GPU-1", gonvml.PSTATE_8, gonvml.SUCCESS),
	))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("got %d measurements, want 2", len(ms))
	}

	for i, want := range []int64{0, 8} {
		m := expectGPUMeasurement(t, ms, i)
		if m.GPUPstate != want {
			t.Errorf("ms[%d].GPUPstate = %d, want %d", i, m.GPUPstate, want)
		}
		if errs := m.Errors(); len(errs) != 0 {
			t.Errorf("ms[%d].Errors() = %v, want empty", i, errs)
		}
	}
}

// A PSTATE_UNKNOWN reading delivered with SUCCESS flags gpu_pstate: the
// driver answered but cannot determine the state, so there is no P-level
// to evaluate. The error is a plain error, not *nvml.Error — the NVML
// call itself did not fail.
func TestMeasureGPUs_PstateUnknownFlagsSignal(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(
		pstateDevice("GPU-0", gonvml.PSTATE_UNKNOWN, gonvml.SUCCESS),
	))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry", errs)
	}
	if want := []string{gpu.SignalGPUPstate}; !slices.Equal(errs[0].FailedSignals(), want) {
		t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
	}
	if _, ok := errs[0].Err().(*nvml.Error); ok {
		t.Errorf("Err = %v, want a plain error, not *nvml.Error (the NVML call succeeded)", errs[0].Err())
	}
}

// A failed performance-state read flags gpu_pstate alone — the GPU is
// still identified so the verdict names the affected device, and the
// sibling gpu_id signal stays trustworthy. ERROR_NOT_SUPPORTED is a
// failure like any other: a GPU that cannot report its performance
// state is a driver fault, not hardware shape.
func TestMeasureGPUs_PstateReadFailureFlagsSignal(t *testing.T) {
	for _, ret := range []gonvml.Return{gonvml.ERROR_UNKNOWN, gonvml.ERROR_NOT_SUPPORTED} {
		t.Run(ret.String(), func(t *testing.T) {
			b := nvml.NewWithLibrary(pstateLib(
				pstateDevice("GPU-0", gonvml.PSTATE_UNKNOWN, ret),
			))
			t.Cleanup(func() { b.Close() })

			ms, err := b.MeasureGPUs()
			if err != nil {
				t.Fatalf("MeasureGPUs: %v", err)
			}
			m := expectGPUMeasurement(t, ms, 0)
			if m.GPUID != "GPU-0" {
				t.Errorf("GPUID = %q, want GPU-0 (errored read must keep the entity id)", m.GPUID)
			}
			errs := m.Errors()
			if len(errs) != 1 {
				t.Fatalf("Errors() = %v, want 1 entry", errs)
			}
			if want := []string{gpu.SignalGPUPstate}; !slices.Equal(errs[0].FailedSignals(), want) {
				t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
			}
			nvmlErr, ok := errs[0].Err().(*nvml.Error)
			if !ok {
				t.Fatalf("Err = %T (%v), want *nvml.Error", errs[0].Err(), errs[0].Err())
			}
			if nvmlErr.Op != "DeviceGetPerformanceState" {
				t.Errorf("Op = %q, want DeviceGetPerformanceState", nvmlErr.Op)
			}
		})
	}
}
