// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml_test

import (
	"cmp"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// TestGPUComputeProcessParity drives gpu_compute_apps through the legacy NVML
// probe and through collect's NVML process sensor beside the DCGM fake, as the
// stock CLI registers them. Both paths read the test process's own procfs
// entries for cmdlines.
//
// Expected difference, listed in advance: a process NVML lists but procfs
// cannot identify keeps a `pid=N` line on the legacy path and is left out of
// collect's cmdlines. The text is diagnostic; the count and verdict match.
func TestGPUComputeProcessParity(t *testing.T) {
	self := uint32(os.Getpid())
	const gone = 1<<22 + 7 // above the kernel's largest pid_max
	tests := []struct {
		name        string
		gpus        []gpuState
		textDiffers bool
	}{
		{name: "no processes", gpus: []gpuState{{}}},
		{name: "own process", gpus: []gpuState{{pids: []uint32{self}}}},
		{name: "process list fails", gpus: []gpuState{{processesFail: true}}},
		{name: "process gone", gpus: []gpuState{{pids: []uint32{gone}}}, textDiffers: true},
		{name: "two GPUs, one busy", gpus: []gpuState{{}, {pids: []uint32{self}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy := measure(t, toolkit.NewGPUSensor(nvml.NewWithLibrary(nvmlLibrary(tt.gpus, false, nil))))
			bridged := measure(t, bridgedGPUSensor(t, collectCycle(t, time.Minute),
				dcgmFake{obs: dcgmObservations(tt.gpus, false)}, processSensor(tt.gpus, nil)))

			l, c := processFacts(t, legacy), processFacts(t, bridged)
			if tt.textDiffers {
				for i := range c {
					if c[i].cmdlines != "" {
						t.Errorf("collect cmdlines = %q, want none for an unidentified process", c[i].cmdlines)
					}
					l[i].cmdlines, c[i].cmdlines = "", ""
				}
			}
			if !reflect.DeepEqual(l, c) {
				t.Errorf("process facts differ:\n legacy=%+v\n collect=%+v", l, c)
			}
			if l, c := computeAppsVerdicts(t, legacy...), computeAppsVerdicts(t, bridged...); !reflect.DeepEqual(l, c) {
				t.Errorf("verdicts differ:\n legacy=%v\n collect=%v", l, c)
			}
		})
	}
}

// TestGPUCollectTwoSourceTimeout stalls the collect GPU sources past the
// cycle timeout. When both stall, as one wedged driver would stall them, every
// check must recommend retrying collection, as the legacy sensor's timeout
// does. When only DCGM stalls, only the DCGM-backed checks may fail.
func TestGPUCollectTwoSourceTimeout(t *testing.T) {
	// Long enough for the NVML process sensor's host procfs scan; the stalled
	// sources stay blocked until release, so only wall time grows.
	const timeout = 500 * time.Millisecond
	retry := health.Drain(health.RemediationActionRetryCollection)
	for _, tt := range []struct {
		name      string
		nvmlStall bool
	}{
		{name: "DCGM and NVML stall", nvmlStall: true},
		{name: "only DCGM stalls"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			var nvmlBlock <-chan struct{}
			if tt.nvmlStall {
				nvmlBlock = release
			}
			gpus := []gpuState{{}}
			bridged := measure(t, bridgedGPUSensor(t, collectCycle(t, timeout),
				dcgmFake{obs: dcgmObservations(gpus, false), block: release}, processSensor(gpus, nvmlBlock)))

			for _, v := range slices.Concat(parityVerdicts(t, evaluateGPU(t, bridged...)), computeAppsVerdicts(t, bridged...)) {
				wantRetry := tt.nvmlStall || v.check != "gpu_compute_apps"
				switch {
				case wantRetry && (v.severity != health.Critical || !v.action.Equal(retry)):
					t.Errorf("%+v, want CRITICAL with retry collection", v)
				case !wantRetry && v.severity != health.Pass:
					t.Errorf("%+v, want PASS: NVML answered", v)
				}
			}
		})
	}
}

// processInfos is what NVML reports for s's compute processes.
func processInfos(s gpuState) ([]gonvml.ProcessInfo, gonvml.Return) {
	if s.processesFail {
		return nil, gonvml.ERROR_UNKNOWN
	}
	infos := make([]gonvml.ProcessInfo, len(s.pids))
	for i, pid := range s.pids {
		infos[i] = gonvml.ProcessInfo{Pid: pid, UsedGpuMemory: 1 << 20}
	}
	return infos, gonvml.SUCCESS
}

// processDevice is collect's NVML view of one fixture GPU.
type processDevice struct {
	device.Device
	uuid  string
	state gpuState
}

func (d processDevice) UUID() string { return d.uuid }
func (d processDevice) GetComputeRunningProcesses() ([]gonvml.ProcessInfo, gonvml.Return) {
	return processInfos(d.state)
}

// processInstance is a collect NVML instance over fixture GPUs. A non-nil
// block stalls reads until closed.
type processInstance struct {
	block   <-chan struct{}
	devices map[string]device.Device
}

func (*processInstance) NVMLExists() bool                    { return true }
func (i *processInstance) Devices() map[string]device.Device { return i.devices }
func (*processInstance) Refresh() error                      { return nil }
func (*processInstance) Shutdown() error                     { return nil }
func (*processInstance) InitError() error                    { return nil }

func (i *processInstance) Read(read func(map[string]device.Device) error) error {
	if i.block != nil {
		<-i.block
	}
	return read(i.devices)
}

var _ nvmlsource.Instance = (*processInstance)(nil)

// processSensor is collect's NVML sensor with cmdline diagnostics, as the
// stock CLI builds it, over gpus.
func processSensor(gpus []gpuState, block <-chan struct{}) collect.Sensor {
	inst := &processInstance{block: block, devices: make(map[string]device.Device)}
	for i, s := range gpus {
		inst.devices[gpuUUID(i)] = processDevice{uuid: gpuUUID(i), state: s}
	}
	return nvmlsensor.NewWithOptions(inst, nvmlsensor.WithProcessCmdlineDiagnostics(true))
}

// processFact is the part of a GPU measurement gpu_compute_apps reads.
type processFact struct {
	id       string
	cmdlines string
	failed   []string
	count    int64
}

func processFacts(t *testing.T, measurements []health.Measurement) []processFact {
	t.Helper()
	var out []processFact
	for _, m := range measurements {
		g, ok := m.(gpu.GPUMeasurement)
		if !ok {
			t.Fatalf("got %T, want gpu.GPUMeasurement", m)
		}
		var failed []string
		for _, e := range g.Errors() {
			failed = append(failed, slices.DeleteFunc(slices.Clone(e.FailedSignals()), func(s string) bool { return !slices.Contains(computeSignals, s) })...)
		}
		slices.Sort(failed)
		if failed = slices.Compact(failed); len(failed) == 0 {
			failed = nil
		}
		out = append(out, processFact{id: g.GPUID, cmdlines: g.ComputeProcessCmdlines, failed: failed, count: g.ComputeProcessCount})
	}
	slices.SortFunc(out, func(a, b processFact) int { return cmp.Compare(a.id, b.id) })
	return out
}

// computeAppsVerdicts evaluates gpu_compute_apps, which runs in tenant guests.
func computeAppsVerdicts(t *testing.T, measurements ...health.Measurement) []parityVerdict {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	if err := cat.Register(gpu.GPUComputeApps()); err != nil {
		t.Fatal(err)
	}
	verdict, err := cat.EvaluateAll(measurements...)
	if err != nil {
		t.Fatal(err)
	}
	return parityVerdicts(t, verdict)
}
