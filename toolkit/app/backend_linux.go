// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"context"
	"errors"
	"sync"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// nvmlHost is the production hostBackend: one [nvml.NVMLBackend] feeding the
// legacy NVLink sensor, the collect GPU sensors, and the collect NVML GPU
// inventory that architecture discovery and the node GPU count share. The
// legacy backend is wrapped in a [toolkit.LazyGPUBackend] so its NVML library
// open is deferred into Collect's per-sensor watchdog — a wedged driver or a
// missing libnvidia-ml then trips the watchdog and degrades the NVLink sensor
// (while the independent Xid sensor still runs) instead of hanging before any
// verdict — and so close skips shutdown while a timed-out Measure may still be
// parked inside the cgo call. The inventory and the compute-process sensor
// share one collect NVML instance, opened on first use under the cycle's
// timeout.
//
// The GPU domain reads DCGM through a hostengine already running on the node,
// and NVML only for compute processes. Neither falls back to the other: an
// unreachable hostengine fails the DCGM-backed GPU checks.
//
// b is the raw backend the probe accessor ([App.NVMLBackend]) hands out; lazy
// wraps it.
type nvmlHost struct {
	b         *nvml.NVMLBackend
	lazy      toolkit.MultiBackend
	inventory *gpuinventory.Inventory
	dcgm      *dcgmsource.Source
	// inst is the collect NVML instance, once opened; processes reads through it.
	inst      nvmlsource.Instance
	processes collect.Sensor
	gpu       []collect.Sensor
	mu        sync.Mutex
}

func newHostBackend() hostBackend {
	b := nvml.New()
	h := &nvmlHost{
		b:    b,
		lazy: toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) { return b, nil }),
		dcgm: dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{}),
	}
	// The signal set is fixed, so New fails only on a programming error that
	// the app tests catch; it records the watch without calling DCGM.
	dcgm, err := dcgmsensor.New(h.dcgm, dcgmGPUSignals.IDs()...)
	if err != nil {
		panic(err)
	}
	h.gpu = []collect.Sensor{dcgm, processSensor{host: h}}
	h.inventory = gpuinventory.New(h.openInventory)
	return h
}

// instance opens the shared collect NVML instance once. The open runs outside
// mu, so one wedged under a timeout cannot block close.
func (h *nvmlHost) instance() (nvmlsource.Instance, error) {
	h.mu.Lock()
	inst := h.inst
	h.mu.Unlock()
	if inst != nil {
		return inst, nil
	}
	inst, err := nvmlsource.New()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inst != nil {
		_ = inst.Shutdown()
		return h.inst, nil
	}
	h.inst = inst
	return inst, nil
}

func (h *nvmlHost) openInventory() (gpuinventory.Reader, error) {
	inst, err := h.instance()
	if err != nil {
		return nil, err
	}
	return gpuinventory.NVML(inst), nil
}

// nvmlProcessSensor returns the collect NVML sensor over the shared instance.
func (h *nvmlHost) nvmlProcessSensor() (collect.Sensor, error) {
	inst, err := h.instance()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.processes == nil {
		h.processes = nvmlsensor.NewWithOptions(inst, nvmlsensor.WithProcessCmdlineDiagnostics(true))
	}
	return h.processes, nil
}

func (h *nvmlHost) gpuSensors() []collect.Sensor          { return h.gpu }
func (h *nvmlHost) nvlinkBackend() toolkit.NVLinkBackend  { return h.lazy }
func (h *nvmlHost) gpuInventory() *gpuinventory.Inventory { return h.inventory }

// close leaves the collect NVML instance and the DCGM client open while a
// timed-out native call still holds them; the process exits shortly after.
func (h *nvmlHost) close() error {
	err := h.lazy.Close()
	if closeErr := h.dcgm.Close(); closeErr != nil && !errors.Is(closeErr, dcgmsource.ErrInUse) {
		err = errors.Join(err, closeErr)
	}
	h.mu.Lock()
	inst := h.inst
	h.mu.Unlock()
	if inst != nil {
		if shutdownErr := inst.Shutdown(); shutdownErr != nil && !errors.Is(shutdownErr, nvmlsource.ErrInUse) {
			err = errors.Join(err, shutdownErr)
		}
	}
	return err
}

// processSensor declares only the compute-process signals of collect's NVML
// sensor, which DCGM cannot supply, so it does not overlap the DCGM sensor.
type processSensor struct{ host *nvmlHost }

func (processSensor) ID() string               { return nvmlsensor.ID }
func (processSensor) Signals() collect.Signals { return processGPUSignals }

func (s processSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	sensor, err := s.host.nvmlProcessSensor()
	if err != nil {
		return nil, err
	}
	return sensor.Measure(ctx, opts...)
}

// One NVMLBackend backs the legacy NVLink sensor, and WithGPUBackend and
// WithNodeBackend overrides may still use it: it implements each narrow
// per-domain interface (and MultiBackend for the lazy wrapper).
var (
	_ toolkit.GPUBackend    = (*nvml.NVMLBackend)(nil)
	_ toolkit.NodeBackend   = (*nvml.NVMLBackend)(nil)
	_ toolkit.NVLinkBackend = (*nvml.NVMLBackend)(nil)
	_ toolkit.MultiBackend  = (*nvml.NVMLBackend)(nil)
)

// NVMLBackend returns the App's built-in NVML backend. Layer custom per-GPU
// signals onto it with WithGPUProbe / WithNVLinkProbe and install the result
// via the matching setter:
//
//	a := app.New()
//	a.WithGPUBackend(a.NVMLBackend().WithGPUProbe(eccProbe{}))
//
// A WithGPUBackend override replaces the built-in DCGM and NVML collect
// sensors for the GPU domain with the legacy GPU sensor over that backend.
//
// It is defined only on linux: the per-device probe type ([nvml.DeviceProber])
// is cgo/NVML-specific, and a consumer's probe code is linux-tagged too.
func (a *App) NVMLBackend() *nvml.NVMLBackend {
	return a.host.(*nvmlHost).b
}
