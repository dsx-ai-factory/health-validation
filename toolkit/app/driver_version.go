// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// discoverDriverVersion prefers the proc read and falls back to NVML. The
// proc read is a file read with no library dependency; NVML covers a
// container given the library and device nodes but not the driver's proc
// tree.
func discoverDriverVersion(fromProc func() (health.DriverVersion, bool), fromNVML func() (string, error)) (health.DriverVersion, bool) {
	if v, ok := fromProc(); ok {
		return v, true
	}
	raw, err := fromNVML()
	if err != nil {
		return health.DriverVersion{}, false
	}
	return health.ParseDriverVersion(raw)
}
