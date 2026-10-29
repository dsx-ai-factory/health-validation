// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// DriverVersion reports the NVIDIA driver version snapshot records, or false
// when no entry carries a parseable one. The version is system-wide, so the
// first entry that has it serves.
func DriverVersion(snapshot *observation.GPUInventorySnapshot) (health.DriverVersion, bool) {
	for _, entry := range snapshot.GetInventory().GetEntries() {
		if raw := entry.GetGpu().GetDriverVersion(); raw != "" {
			return health.ParseDriverVersion(raw)
		}
	}
	return health.DriverVersion{}, false
}
