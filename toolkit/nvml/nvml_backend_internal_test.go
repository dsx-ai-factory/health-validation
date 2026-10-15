// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml

import (
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
)

// These tests exercise devices directly because it is the unit of
// behavior they cover (lazy load, count failure, errored-slot
// preservation, caching). Public API behavior is covered separately
// in nvml_backend_test.go.

func TestDevices_PreservesHandles(t *testing.T) {
	dev0, dev1 := &mock.Device{}, &mock.Device{}
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 2, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(n int) (gonvml.Device, gonvml.Return) {
			return []gonvml.Device{dev0, dev1}[n], gonvml.SUCCESS
		},
	}

	c := NewWithLibrary(lib)
	defer c.Close()

	devs, err := c.devices()
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if got := len(devs); got != 2 {
		t.Errorf("device count = %d, want 2", got)
	}
	if devs[0] != dev0 || devs[1] != dev1 {
		t.Error("device handles not preserved")
	}
}

func TestDevices_CountFailure(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:     func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc: func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) {
			return 0, gonvml.ERROR_UNKNOWN
		},
	}
	c := NewWithLibrary(lib)
	defer c.Close()
	if _, err := c.devices(); err == nil {
		t.Fatal("expected error from devices on count failure, got nil")
	}
}

// A device whose handle cannot be retrieved is preserved in the slice
// as an erroredDevice placeholder so Measure can render the slot as a
// missing measurement. The healthy GPU still enumerates normally.
func TestDevices_HandleFailureBecomesErroredDevice(t *testing.T) {
	dev0 := &mock.Device{
		GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-0", gonvml.SUCCESS },
	}
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 2, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(n int) (gonvml.Device, gonvml.Return) {
			if n == 0 {
				return dev0, gonvml.SUCCESS
			}
			return nil, gonvml.ERROR_GPU_IS_LOST
		},
	}
	c := NewWithLibrary(lib)
	defer c.Close()

	devs, err := c.devices()
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2 (errored slot must be preserved)", len(devs))
	}
	if uuid, ret := devs[0].GetUUID(); ret != gonvml.SUCCESS || uuid != "GPU-0" {
		t.Errorf("devs[0].GetUUID() = (%q, %v), want (\"GPU-0\", SUCCESS)", uuid, ret)
	}
	if _, ok := devs[1].(erroredDevice); !ok {
		t.Errorf("devs[1] = %T, want erroredDevice", devs[1])
	}
}

// devices caches its result on the receiver; once populated, the
// backend must not re-query NVML on subsequent calls.
func TestDevices_Caches(t *testing.T) {
	var countCalls int
	lib := &mock.Interface{
		InitFunc:     func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc: func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) {
			countCalls++
			return 0, gonvml.SUCCESS
		},
	}
	c := NewWithLibrary(lib)
	defer c.Close()

	if _, err := c.devices(); err != nil {
		t.Fatalf("devices: %v", err)
	}
	if _, err := c.devices(); err != nil {
		t.Fatalf("devices (second call): %v", err)
	}
	if countCalls != 1 {
		t.Errorf("DeviceGetCount invoked %d times, want 1 (result should be cached)", countCalls)
	}
}
