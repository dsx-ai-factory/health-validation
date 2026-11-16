// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func withDriver(entry *observation.GPUInventoryEntry, version string) *observation.GPUInventoryEntry {
	entry.Gpu.DriverVersion = &version
	return entry
}

func TestDriverVersion(t *testing.T) {
	for _, tc := range []struct {
		snapshot *observation.GPUInventorySnapshot
		name     string
		want     string
	}{
		{name: "recorded", snapshot: inventory(withDriver(gpuEntry(0, "NVIDIA GB200"), "575.51.03")), want: "575.51.03"},
		{name: "failed slot still carries it", snapshot: inventory(withDriver(failedEntry(0, handleOperation), "570.86.15")), want: "570.86.15"},
		{name: "first entry without it", snapshot: inventory(gpuEntry(0, "NVIDIA GB200"), withDriver(gpuEntry(1, "NVIDIA GB200"), "580.65.06")), want: "580.65.06"},
		{name: "not recorded", snapshot: inventory(gpuEntry(0, "NVIDIA GB200"))},
		{name: "unparseable", snapshot: inventory(withDriver(gpuEntry(0, "NVIDIA GB200"), "r575"))},
		{name: "enumeration failure", snapshot: enumerationFailure()},
		{name: "no GPUs", snapshot: inventory()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DriverVersion(tc.snapshot)
			if ok != (tc.want != "") || got.Raw != tc.want {
				t.Fatalf("DriverVersion = %+v, %v; want %q", got, ok, tc.want)
			}
		})
	}
}
