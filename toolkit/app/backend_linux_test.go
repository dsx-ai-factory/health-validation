// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

// The shared NVML sensor offers every NVML signal DCGM does not supply: the
// stock CLI's process and NVLink signals, and others such as ECC that only a
// caller adapter requests.
func TestNVMLGPUSignalsAreNVMLMinusDCGM(t *testing.T) {
	ids := nvmlGPUSignals.IDs()
	for _, id := range slices.Concat(dcgmGPUSignals, dcgmNVLinkSignals).IDs() {
		if slices.Contains(ids, id) {
			t.Errorf("%s is declared by both the DCGM and NVML built-ins", id)
		}
	}
	for _, id := range append(slices.Concat(processGPUSignals, nvmlNVLinkSignals).IDs(), gpusignals.ECCDoubleBitVolatileTotal.ID(), gpusignals.RowRemapPending.ID()) {
		if !slices.Contains(ids, id) {
			t.Errorf("NVML built-in does not declare %s", id)
		}
	}
}

// virtualizationDevice fails any unrelated query through its embedded device.
type virtualizationDevice struct {
	device.Device
	mode  nvml.GpuVirtualizationMode
	ret   nvml.Return
	calls int
}

func (d *virtualizationDevice) GetVirtualizationMode() (nvml.GpuVirtualizationMode, nvml.Return) {
	d.calls++
	return d.mode, d.ret
}

// TestGPUVirtualization checks every documented mode and failure to classify.
func TestGPUVirtualization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    nvml.GpuVirtualizationMode
		ret     nvml.Return
		virtual bool
		failed  bool
	}{
		{name: "physical", mode: nvml.GPU_VIRTUALIZATION_MODE_NONE},
		{name: "passthrough", mode: nvml.GPU_VIRTUALIZATION_MODE_PASSTHROUGH},
		{name: "vGPU", mode: nvml.GPU_VIRTUALIZATION_MODE_VGPU, virtual: true},
		{name: "vGPU host", mode: nvml.GPU_VIRTUALIZATION_MODE_HOST_VGPU},
		{name: "vSGA host", mode: nvml.GPU_VIRTUALIZATION_MODE_HOST_VSGA},
		{name: "unknown mode", mode: 99, failed: true},
		{name: "unsupported", ret: nvml.ERROR_NOT_SUPPORTED, failed: true},
		{name: "read failure", ret: nvml.ERROR_UNKNOWN, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev := &virtualizationDevice{mode: tc.mode, ret: tc.ret}
			host := &nvmlHost{inst: &fakeNVMLInstance{devices: map[string]device.Device{"GPU-0": dev}}}
			virtual, err := host.gpuVirtualization(context.Background(), "GPU-0")
			if virtual != tc.virtual || (err != nil) != tc.failed || dev.calls != 1 {
				t.Fatalf("virtual=%v err=%v calls=%d", virtual, err, dev.calls)
			}
			if _, err := host.gpuVirtualization(context.Background(), "GPU-missing"); err == nil {
				t.Fatal("missing GPU classified as physical")
			}
		})
	}
}

// TestNVMLReadGate bounds waiting without entering another native operation.
func TestNVMLReadGate(t *testing.T) {
	host := &nvmlHost{}
	release, err := host.acquireNVMLRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if unlock, waitErr := host.acquireNVMLRead(ctx); !errors.Is(waitErr, context.DeadlineExceeded) {
		if unlock != nil {
			unlock()
		}
		release()
		t.Fatalf("gate error=%v, want deadline", waitErr)
	}
	release()
	release, err = host.acquireNVMLRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	canceledContext, stop := context.WithCancel(context.Background())
	stop()
	if unlock, canceledErr := host.acquireNVMLRead(canceledContext); !errors.Is(canceledErr, context.Canceled) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("gate error=%v, want canceled", canceledErr)
	}
}
