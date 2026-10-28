// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build !linux || android

package app

import (
	"fmt"
	"runtime"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// stubHost is the off-linux hostBackend. The NVML backend is gated on
// `linux && !android` (cgo + libnvidia-ml), so off linux the host backend
// errors at Measure — keeping the command buildable (and `version` runnable)
// while surfacing a clear runtime error if `run` is invoked here.
type stubHost struct{}

func newHostBackend() hostBackend { return stubHost{} }

func (stubHost) gpuBackend() toolkit.GPUBackend       { return unavailableBackend{} }
func (stubHost) nvlinkBackend() toolkit.NVLinkBackend { return unavailableBackend{} }

// stubInventory fails to open off-linux, so `run` reports an architecture
// discovery failure and the node GPU count is unobservable.
var stubInventory = gpuinventory.New(func() (gpuinventory.Reader, error) {
	return nil, fmt.Errorf("NVML GPU inventory is only available on linux (current platform: %s)", runtime.GOOS)
})

func (stubHost) gpuInventory() *gpuinventory.Inventory { return stubInventory }
func (stubHost) close() error                          { return nil }

// unavailableBackend implements every per-domain backend interface on a
// platform with no NVML support; every measure method returns a clear error so
// `run` fails loudly rather than silently producing an empty verdict.
type unavailableBackend struct{}

func unavailable() ([]health.Measurement, error) {
	return nil, fmt.Errorf("NVML backend is only available on linux (current platform: %s)", runtime.GOOS)
}

func (unavailableBackend) MeasureGPUs() ([]health.Measurement, error)   { return unavailable() }
func (unavailableBackend) MeasureNode() ([]health.Measurement, error)   { return unavailable() }
func (unavailableBackend) MeasureNVLink() ([]health.Measurement, error) { return unavailable() }
