// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// nvmlHost is the production hostBackend: the collect GPU sensors and the
// collect NVML GPU inventory that architecture discovery and the node GPU
// count share. The inventory and the NVML sensor share one collect NVML
// instance, opened on first use under the cycle's timeout, so a wedged driver
// or a missing libnvidia-ml fails collection instead of hanging before any
// verdict.
//
// The GPU domain reads DCGM through a hostengine already running on the node,
// and NVML only for compute processes. The NVLink domain reads fabric
// registration from DCGM, and links, P2P, and fabric health from NVML. Neither
// falls back to the other: an unreachable hostengine fails the DCGM-backed
// checks.
type nvmlHost struct {
	inventory *gpuinventory.Inventory
	dcgm      *dcgmsource.Source
	// inst is the collect NVML instance, once opened; processes reads through it.
	inst       nvmlsource.Instance
	nvmlSensor collect.Sensor
	gpu        []collect.Sensor
	mu         sync.Mutex
}

func newHostBackend() hostBackend {
	h := &nvmlHost{
		// v0.3's NVML sensor read the driver live; DCGM reads live to match.
		dcgm: dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{LiveMetrics: true}),
	}
	// The signal set is fixed, so New fails only on a programming error that
	// the app tests catch; it records the watch without calling DCGM.
	dcgm, err := dcgmsensor.New(h.dcgm, slices.Concat(dcgmGPUSignals, dcgmNVLinkSignals).IDs()...)
	if err != nil {
		panic(err)
	}
	h.gpu = []collect.Sensor{dcgm, nvmlGPUSensor{host: h}}
	h.inventory = gpuinventory.New(gpuinventory.OpenNVML(h.instance))
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

// sharedNVMLSensor returns the collect NVML sensor over the shared instance.
func (h *nvmlHost) sharedNVMLSensor() (collect.Sensor, error) {
	inst, err := h.instance()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.nvmlSensor == nil {
		h.nvmlSensor = nvmlsensor.NewWithOptions(inst, nvmlsensor.WithProcessCmdlineDiagnostics(true))
	}
	return h.nvmlSensor, nil
}

func (h *nvmlHost) gpuSensors() []collect.Sensor          { return h.gpu }
func (h *nvmlHost) gpuInventory() *gpuinventory.Inventory { return h.inventory }

// close leaves the collect NVML instance and the DCGM client open while a
// timed-out native call still holds them; the process exits shortly after.
func (h *nvmlHost) close() error {
	var err error
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

// nvmlGPUSignals is every signal collect's NVML sensor reads except those the
// stock CLI takes from DCGM. A caller adapter can request any of them and the
// shared NVML instance supplies it, under its native-call gate, without the
// caller registering a sensor. The stock CLI requests processGPUSignals and
// nvmlNVLinkSignals.
var nvmlGPUSignals = slices.DeleteFunc(
	nvmlsensor.NewWithOptions(nil, nvmlsensor.WithProcessCmdlineDiagnostics(true)).Signals(),
	func(s collect.Signal) bool {
		return slices.Contains(slices.Concat(dcgmGPUSignals, dcgmNVLinkSignals).IDs(), s.ID())
	},
)

// nvmlGPUSensor is the shared collect NVML sensor, declaring nvmlGPUSignals so
// it does not overlap the DCGM sensor.
type nvmlGPUSensor struct{ host *nvmlHost }

func (nvmlGPUSensor) ID() string               { return nvmlsensor.ID }
func (nvmlGPUSensor) Signals() collect.Signals { return nvmlGPUSignals }

func (s nvmlGPUSensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	sensor, err := s.host.sharedNVMLSensor()
	if err != nil {
		return nil, err
	}
	return sensor.Measure(ctx, opts...)
}
