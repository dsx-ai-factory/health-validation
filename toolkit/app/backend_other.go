// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build !linux || android

package app

import (
	"context"
	"fmt"
	"runtime"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// stubHost is the off-linux hostBackend. The NVML backend is gated on
// `linux && !android` (cgo + libnvidia-ml), so off linux the host backend
// errors at Measure — keeping the command buildable (and `version` runnable)
// while surfacing a clear runtime error if `run` is invoked here.
type stubHost struct{}

func newHostBackend() hostBackend { return stubHost{} }

func (stubHost) gpuSensors() []collect.Sensor { return []collect.Sensor{unavailableGPUSensor{}} }

// unavailableGPUSensor declares every GPU collect signal, a superset of what
// the linux DCGM and NVML sensors declare, and fails at Measure, so GPU checks
// and caller adapters report why rather than going unconfigured.
type unavailableGPUSensor struct{}

func (unavailableGPUSensor) ID() string               { return "gpu.unavailable" }
func (unavailableGPUSensor) Signals() collect.Signals { return gpusignals.All() }

func (unavailableGPUSensor) Measure(context.Context, ...collect.OpOption) (*observation.ObservationBatch, error) {
	return nil, fmt.Errorf("DCGM and NVML GPU collection are only available on linux (current platform: %s)", runtime.GOOS)
}

// stubInventory fails to open off-linux, so `run` reports an architecture
// discovery failure and the node GPU count is unobservable.
var stubInventory = gpuinventory.New(func() (gpuinventory.Reader, error) {
	return nil, fmt.Errorf("NVML GPU inventory is only available on linux (current platform: %s)", runtime.GOOS)
})

func (stubHost) gpuInventory() *gpuinventory.Inventory { return stubInventory }
func (stubHost) close() error                          { return nil }
