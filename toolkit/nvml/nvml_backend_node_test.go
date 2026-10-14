// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml_test

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// nodeOnlyLib returns a mock.Interface with count devices and the given
// DeviceGetCount Return. MeasureNode only enumerates and counts devices,
// so the mock devices need no per-query stubs.
func nodeOnlyLib(count int, ret gonvml.Return) gonvml.Interface {
	return &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return count, ret },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{}, gonvml.SUCCESS
		},
	}
}

// expectNodeMeasurement asserts the slice carries exactly one
// NodeMeasurement and returns it.
func expectNodeMeasurement(t *testing.T, ms []health.Measurement) node.NodeMeasurement {
	t.Helper()
	if len(ms) != 1 {
		t.Fatalf("got %d measurements, want 1", len(ms))
	}
	nm, ok := ms[0].(node.NodeMeasurement)
	if !ok {
		t.Fatalf("measurement is not NodeMeasurement: %T", ms[0])
	}
	return nm
}

func TestMeasureNode_Healthy(t *testing.T) {
	b := nvml.NewWithLibrary(nodeOnlyLib(8, gonvml.SUCCESS))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureNode()
	if err != nil {
		t.Fatalf("MeasureNode: %v", err)
	}
	nm := expectNodeMeasurement(t, ms)
	if errs := nm.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty", errs)
	}
	if nm.GPUCount != 8 {
		t.Errorf("GPUCount = %d, want 8", nm.GPUCount)
	}
}

// A DeviceGetCount failure flags every node-level signal via a typed
// *nvml.Error: without an enumerated device list gpu_count cannot be
// trusted, so any node-level check whose RequiredSignals overlap with
// the failed entry must fail Critical.
func TestMeasureNode_CountFailureFlagsAllSignals(t *testing.T) {
	b := nvml.NewWithLibrary(nodeOnlyLib(0, gonvml.ERROR_UNKNOWN))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureNode()
	if err != nil {
		t.Fatalf("MeasureNode: %v", err)
	}
	nm := expectNodeMeasurement(t, ms)
	errs := nm.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry", errs)
	}
	want := []string{node.SignalGPUCount}
	if !slices.Equal(errs[0].FailedSignals(), want) {
		t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
	}
	nvmlErr, ok := errs[0].Err().(*nvml.Error)
	if !ok {
		t.Fatalf("Err = %T (%v), want *nvml.Error", errs[0].Err(), errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetCount" {
		t.Errorf("Op = %q, want DeviceGetCount", nvmlErr.Op)
	}
}

// A DeviceGetHandleByIndex failure excludes that slot from the visible
// gpu_count without recording an error: the count is honest (the lost
// slot is simply not counted) and the gpu_count check surfaces the
// shortfall against expected_gpu_count.
func TestMeasureNode_HandleFailureExcludesGPUCount(t *testing.T) {
	const total = 4
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return total, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(i int) (gonvml.Device, gonvml.Return) {
			// Indices 1 and 3 fall off the bus.
			if i == 1 || i == 3 {
				return nil, gonvml.ERROR_GPU_IS_LOST
			}
			return &mock.Device{}, gonvml.SUCCESS
		},
	}
	b := nvml.NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureNode()
	if err != nil {
		t.Fatalf("MeasureNode: %v", err)
	}
	nm := expectNodeMeasurement(t, ms)
	if nm.GPUCount != 2 {
		t.Errorf("GPUCount = %d, want 2 (4 enumerated, 2 fell off the bus)", nm.GPUCount)
	}
	if errs := nm.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none (a lost slot is excluded from the count, not flagged)", errs)
	}
}
