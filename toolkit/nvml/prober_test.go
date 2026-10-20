// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package nvml_test

import (
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// captureNVLink is the NVLink-domain analogue of captureProbe: it records the
// uuids it sees and emits a real NVLink measurement, standing in for a
// downstream's custom NVLink per-device reader.
type captureNVLink struct {
	uuids []string
}

func (p *captureNVLink) Measure(pc nvml.ProbeContext) health.Measurement {
	p.uuids = append(p.uuids, pc.Self.UUID)
	return nvlink.NVLinkMeasurement{GPUID: pc.Self.UUID}
}

func (p *captureNVLink) Errored(id string, err error) health.Measurement {
	return nvlink.ErroredNVLink(id, err, nvlink.MeasurementSignals()...)
}

// captureProbe records the id/uuid it is called with and emits a real GPU
// measurement so the test can assert on backend-owned id-encoding and loop
// behavior. It stands in for a downstream's custom per-device reader.
type captureProbe struct {
	uuids []string // uuid per Measure call, in order
	ids   []string // id per Errored call, in order
}

func (p *captureProbe) Measure(pc nvml.ProbeContext) health.Measurement {
	p.uuids = append(p.uuids, pc.Self.UUID)
	return gpu.GPUMeasurement{GPUID: pc.Self.UUID}
}

func (p *captureProbe) Errored(id string, err error) health.Measurement {
	p.ids = append(p.ids, id)
	return gpu.ErroredGPU(id, err, gpu.MeasurementSignals()...)
}

// threeSlotLib enumerates three GPUs covering the slot states the backend
// distinguishes: a usable device, a handle-load failure, and a UUID-read
// failure.
func threeSlotLib() *mock.Interface {
	usable := &mock.Device{
		GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-OK", gonvml.SUCCESS },
		GetPerformanceStateFunc: func() (gonvml.Pstates, gonvml.Return) {
			return gonvml.PSTATE_0, gonvml.SUCCESS
		},
		GetFieldValuesFunc:          averagePowerFieldValues(220_000),
		GetPowerManagementLimitFunc: func() (uint32, gonvml.Return) { return 0, gonvml.SUCCESS },
		GetEnforcedPowerLimitFunc:   func() (uint32, gonvml.Return) { return 0, gonvml.SUCCESS },
		GetCurrentClocksEventReasonsFunc: func() (uint64, gonvml.Return) {
			return 0, gonvml.SUCCESS
		},
		GetComputeRunningProcessesFunc: func() ([]gonvml.ProcessInfo, gonvml.Return) {
			return nil, gonvml.SUCCESS
		},
	}
	noUUID := &mock.Device{
		GetUUIDFunc: func() (string, gonvml.Return) { return "", gonvml.ERROR_GPU_IS_LOST },
	}
	return &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 3, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(n int) (gonvml.Device, gonvml.Return) {
			switch n {
			case 0:
				return usable, gonvml.SUCCESS
			case 1:
				return nil, gonvml.ERROR_GPU_IS_LOST // handle load fails
			default:
				return noUUID, gonvml.SUCCESS // handle loads, UUID read fails
			}
		},
	}
}

// TestMeasureGPUs_CustomProber installs a downstream prober via WithProbe and
// confirms MeasureGPUs drives it once per slot with the backend-owned
// id-encoding: the usable slot reaches Measure with its UUID; the handle-load
// and UUID-read failures both reach Errored with id "nvml-index-N". This is
// the probe seam, end to end.
func TestMeasureGPUs_CustomProber(t *testing.T) {
	b := nvml.NewWithLibrary(threeSlotLib())
	t.Cleanup(func() { b.Close() })

	p := &captureProbe{}
	b = b.WithGPUProbe(p)
	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("got %d measurements, want 3 (one per slot)", len(ms))
	}
	if len(p.uuids) != 1 || p.uuids[0] != "GPU-OK" {
		t.Errorf("Measure uuids = %v, want [GPU-OK]", p.uuids)
	}
	wantIDs := []string{"nvml-index-1", "nvml-index-2"}
	if len(p.ids) != len(wantIDs) {
		t.Fatalf("Errored ids = %v, want %v", p.ids, wantIDs)
	}
	for i, want := range wantIDs {
		if p.ids[i] != want {
			t.Errorf("Errored id[%d] = %q, want %q", i, p.ids[i], want)
		}
	}
}

// TestMeasureGPUs_DefaultProber confirms the built-in gpuProbe is wired by
// default: the usable slot yields an identity gpu.GPUMeasurement, and the two
// unresolved slots yield errored measurements carrying the encoded ids.
func TestMeasureGPUs_DefaultProber(t *testing.T) {
	b := nvml.NewWithLibrary(threeSlotLib())
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("got %d measurements, want 3", len(ms))
	}

	want := []struct {
		gpuID     string
		key       health.EntityKey
		hasErrors bool
	}{
		{"GPU-OK", health.VerifiedEntity("GPU-OK"), false},            // usable
		{"nvml-index-1", health.ProvisionalEntity("nvml", "1"), true}, // handle load failed
		{"nvml-index-2", health.ProvisionalEntity("nvml", "2"), true}, // UUID read failed
	}
	for i, w := range want {
		m, ok := ms[i].(gpu.GPUMeasurement)
		if !ok {
			t.Fatalf("ms[%d] = %T, want gpu.GPUMeasurement", i, ms[i])
		}
		if m.GPUID != w.gpuID {
			t.Errorf("ms[%d].GPUID = %q, want %q", i, m.GPUID, w.gpuID)
		}
		if m.EntityKey() != w.key {
			t.Errorf("ms[%d] entity = %v, want %v", i, m.EntityKey(), w.key)
		}
		if gotErr := len(m.Errors()) > 0; gotErr != w.hasErrors {
			t.Errorf("ms[%d] hasErrors = %v, want %v", i, gotErr, w.hasErrors)
		}
	}
}

// TestMeasureGPUs_CountFailure confirms the only enumeration error —
// DeviceGetCount failing — propagates from MeasureGPUs unchanged.
func TestMeasureGPUs_CountFailure(t *testing.T) {
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 0, gonvml.ERROR_UNKNOWN },
	}
	b := nvml.NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	if _, err := b.WithGPUProbe(&captureProbe{}).MeasureGPUs(); err == nil {
		t.Fatal("expected error from MeasureGPUs on count failure, got nil")
	}
}

// TestMeasureNVLink_CustomProber installs a downstream NVLink prober via
// WithNVLinkProbe and confirms MeasureNVLink drives it once per slot with the
// same backend-owned id-encoding as the GPU domain.
func TestMeasureNVLink_CustomProber(t *testing.T) {
	b := nvml.NewWithLibrary(threeSlotLib())
	t.Cleanup(func() { b.Close() })

	p := &captureNVLink{}
	b = b.WithNVLinkProbe(p)
	ms, err := b.MeasureNVLink()
	if err != nil {
		t.Fatalf("MeasureNVLink: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("got %d measurements, want 3 (one per slot)", len(ms))
	}
	if len(p.uuids) != 1 || p.uuids[0] != "GPU-OK" {
		t.Errorf("NVLink Measure uuids = %v, want [GPU-OK]", p.uuids)
	}
}

// TestProbes_DomainScopedNoClobber confirms the two probe seams are
// independent: setting a GPU prober and an NVLink prober (in either chained
// order) leaves both in effect — MeasureGPUs drives the GPU prober and
// MeasureNVLink drives the NVLink prober, neither overwriting the other.
func TestProbes_DomainScopedNoClobber(t *testing.T) {
	b := nvml.NewWithLibrary(threeSlotLib())
	t.Cleanup(func() { b.Close() })

	gp := &captureProbe{}
	np := &captureNVLink{}
	b = b.WithGPUProbe(gp).WithNVLinkProbe(np)

	if _, err := b.MeasureGPUs(); err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	if _, err := b.MeasureNVLink(); err != nil {
		t.Fatalf("MeasureNVLink: %v", err)
	}

	if len(gp.uuids) != 1 || gp.uuids[0] != "GPU-OK" {
		t.Errorf("GPU prober uuids = %v, want [GPU-OK] (GPU prober clobbered?)", gp.uuids)
	}
	if len(np.uuids) != 1 || np.uuids[0] != "GPU-OK" {
		t.Errorf("NVLink prober uuids = %v, want [GPU-OK] (NVLink prober clobbered?)", np.uuids)
	}
}
