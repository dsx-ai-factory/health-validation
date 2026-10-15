// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build !linux || android

package app

import (
	"fmt"
	"runtime"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// stubHost is the off-linux hostBackend. The NVML backend is gated on
// `linux && !android` (cgo + libnvidia-ml), so off linux the host backend
// errors at Measure — keeping the command buildable (and `version` runnable)
// while surfacing a clear runtime error if `run` is invoked here.
type stubHost struct{}

func newHostBackend() hostBackend { return stubHost{} }

func (stubHost) gpuBackend() toolkit.GPUBackend       { return unavailableBackend{} }
func (stubHost) nodeBackend() toolkit.NodeBackend     { return unavailableBackend{} }
func (stubHost) nvlinkBackend() toolkit.NVLinkBackend { return unavailableBackend{} }

// architecture reports no architecture off-linux: there is no NVML to query,
// so evaluation runs without architecture filtering. `run` still fails loudly
// at collection via unavailableBackend, so this never masks a real run.
func (stubHost) architecture() (string, error) { return "", nil }
func (stubHost) close() error                  { return nil }

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
