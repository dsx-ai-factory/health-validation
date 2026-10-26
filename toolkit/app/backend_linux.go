// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"errors"
	"sync"

	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// nvmlHost is the production hostBackend: one [nvml.NVMLBackend] feeding the
// legacy GPU and NVLink sensors from a single device enumeration, and the
// collect NVML GPU inventory that architecture discovery and the node GPU
// count share. The legacy backend is wrapped in a [toolkit.LazyGPUBackend] so
// its NVML library open is deferred into Collect's per-sensor watchdog — a
// wedged driver or a missing libnvidia-ml then trips the watchdog and
// degrades the GPU-side sensors (while the independent Xid sensor still runs)
// instead of hanging before any verdict — and so close skips shutdown while a
// timed-out Measure may still be parked inside the cgo call. The inventory
// opens its NVML instance on first read, under the cycle's timeout, and reads
// only the basic inventory discovery and gpu_count need.
//
// The one lazy backend is shared across both per-domain accessors, so the
// GPU and NVLink sensors report the same resource key and the circuit
// breaker skips them together once it trips. b is the raw backend the probe
// accessor ([App.NVMLBackend]) hands out; lazy wraps it.
type nvmlHost struct {
	b         *nvml.NVMLBackend
	lazy      toolkit.MultiBackend
	inventory *gpuinventory.Inventory
	// inst is the collect NVML instance the inventory opened, if any.
	inst nvmlsource.Instance
	mu   sync.Mutex
}

func newHostBackend() hostBackend {
	b := nvml.New()
	h := &nvmlHost{
		b:    b,
		lazy: toolkit.LazyGPUBackend(func() (toolkit.MultiBackend, error) { return b, nil }),
	}
	h.inventory = gpuinventory.New(h.openInventory)
	return h
}

func (h *nvmlHost) openInventory() (gpuinventory.Reader, error) {
	inst, err := nvmlsource.New()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.inst = inst
	h.mu.Unlock()
	return gpuinventory.NVML(inst), nil
}

func (h *nvmlHost) gpuBackend() toolkit.GPUBackend        { return h.lazy }
func (h *nvmlHost) nvlinkBackend() toolkit.NVLinkBackend  { return h.lazy }
func (h *nvmlHost) gpuInventory() *gpuinventory.Inventory { return h.inventory }

// close leaves the collect NVML instance open while a timed-out native call
// still holds it; the process exits shortly after.
func (h *nvmlHost) close() error {
	err := h.lazy.Close()
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

// One NVMLBackend backs the legacy GPU and NVLink sensors, and a
// WithNodeBackend override may still use it: it implements each narrow
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
// It is defined only on linux: the per-device probe type ([nvml.DeviceProber])
// is cgo/NVML-specific, and a consumer's probe code is linux-tagged too.
func (a *App) NVMLBackend() *nvml.NVMLBackend {
	return a.host.(*nvmlHost).b
}
