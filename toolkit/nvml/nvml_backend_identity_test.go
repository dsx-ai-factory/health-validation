// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml_test

import (
	"maps"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// pciAt returns a GetPciInfo stub for bus 01, device 00 in domain.
func pciAt(domain uint32) func() (gonvml.PciInfo, gonvml.Return) {
	return func() (gonvml.PciInfo, gonvml.Return) {
		return gonvml.PciInfo{Domain: domain, Bus: 1}, gonvml.SUCCESS
	}
}

func identityDevice(uuid string, domain uint32) *mock.Device {
	dev := pstateDevice(uuid, gonvml.PSTATE_0, gonvml.SUCCESS)
	dev.GetPciInfoFunc = pciAt(domain)
	return dev
}

func measuredUUIDs(t *testing.T, b *nvml.NVMLBackend) map[string]string {
	t.Helper()
	if _, err := b.MeasureGPUs(); err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	return b.GPUUUIDsByPCI()
}

func TestGPUUUIDsByPCI_EmptyBeforeProbe(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(identityDevice("GPU-0", 8)))
	t.Cleanup(func() { b.Close() })

	if got := b.GPUUUIDsByPCI(); len(got) != 0 {
		t.Errorf("GPUUUIDsByPCI() = %v, want empty before any measurement", got)
	}
}

func TestGPUUUIDsByPCI_RecordsProbedDevices(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(identityDevice("GPU-0", 8), identityDevice("GPU-1", 9)))
	t.Cleanup(func() { b.Close() })

	want := map[string]string{"0008:01:00": "GPU-0", "0009:01:00": "GPU-1"}
	if got := measuredUUIDs(t, b); !maps.Equal(got, want) {
		t.Errorf("GPUUUIDsByPCI() = %v, want %v", got, want)
	}
}

// The GPU checks do not read the PCI address, so a failed read leaves the
// device's measurement clean.
func TestGPUUUIDsByPCI_SkipsPciInfoFailure(t *testing.T) {
	lost := identityDevice("GPU-1", 9)
	lost.GetPciInfoFunc = func() (gonvml.PciInfo, gonvml.Return) { return gonvml.PciInfo{}, gonvml.ERROR_GPU_IS_LOST }
	b := nvml.NewWithLibrary(pstateLib(identityDevice("GPU-0", 8), lost))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	if errs := expectGPUMeasurement(t, ms, 1).Errors(); len(errs) != 0 {
		t.Errorf("GPU-1 errors = %v, want none", errs)
	}
	if got, want := b.GPUUUIDsByPCI(), map[string]string{"0008:01:00": "GPU-0"}; !maps.Equal(got, want) {
		t.Errorf("GPUUUIDsByPCI() = %v, want %v", got, want)
	}
}

func TestGPUUUIDsByPCI_SkipsUnreadableUUID(t *testing.T) {
	noUUID := identityDevice("", 9)
	noUUID.GetUUIDFunc = func() (string, gonvml.Return) { return "", gonvml.ERROR_GPU_IS_LOST }
	noUUID.GetPciInfoFunc = func() (gonvml.PciInfo, gonvml.Return) {
		t.Error("GetPciInfo called for a GPU whose UUID could not be read")
		return gonvml.PciInfo{}, gonvml.ERROR_UNKNOWN
	}
	b := nvml.NewWithLibrary(pstateLib(identityDevice("GPU-0", 8), noUUID))
	t.Cleanup(func() { b.Close() })

	if got, want := measuredUUIDs(t, b), map[string]string{"0008:01:00": "GPU-0"}; !maps.Equal(got, want) {
		t.Errorf("GPUUUIDsByPCI() = %v, want %v", got, want)
	}
}

func TestGPUUUIDsByPCI_SharedWithProbeCopies(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(identityDevice("GPU-0", 8), identityDevice("GPU-1", 9)))
	t.Cleanup(func() { b.Close() })

	if _, err := b.WithGPUProbe(&captureProbe{}).MeasureGPUs(); err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	if got := b.GPUUUIDsByPCI(); len(got) != 2 {
		t.Errorf("GPUUUIDsByPCI() = %v, want both GPUs probed through the copy", got)
	}
}

func TestGPUUUIDsByPCI_ReturnsCopy(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(identityDevice("GPU-0", 8)))
	t.Cleanup(func() { b.Close() })

	measuredUUIDs(t, b)["0008:01:00"] = "changed"
	if got := b.GPUUUIDsByPCI()["0008:01:00"]; got != "GPU-0" {
		t.Errorf("GPUUUIDsByPCI()[0008:01:00] = %q after mutating a returned map, want GPU-0", got)
	}
}
