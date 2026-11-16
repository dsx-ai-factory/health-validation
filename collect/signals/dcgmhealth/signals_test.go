// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgmhealth

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
)

func TestHealthCatalog(t *testing.T) {
	all := All()
	if len(all) != 1 || all[0].ID() != "dcgm.health" || Report.ID() != all[0].ID() {
		t.Fatalf("All() = %v, want the aggregate dcgm.health signal", all)
	}
	if Report.Unit() != nil {
		t.Fatal("report has a physical unit")
	}
	all[0] = collect.NewSignal("other", "")
	if All()[0].ID() != Report.ID() {
		t.Fatal("mutating All() changed the catalog")
	}
}

func TestNativeSystemMasks(t *testing.T) {
	tests := []struct {
		name string
		got  uint64
		want uint64
	}{
		{name: "PCIe", got: SystemPCIe, want: 1},
		{name: "NVLink", got: SystemNVLink, want: 2},
		{name: "memory", got: SystemMemory, want: 16},
		{name: "InfoROM", got: SystemInfoROM, want: 64},
		{name: "thermal", got: SystemThermal, want: 128},
		{name: "power", got: SystemPower, want: 256},
		{name: "NVSwitch nonfatal", got: SystemNVSwitchNonfatal, want: 1024},
		{name: "NVSwitch fatal", got: SystemNVSwitchFatal, want: 2048},
		{name: "all native", got: SystemAll, want: 0xffffffff},
		{name: "known", got: KnownSystems, want: 3539},
		{name: "GPU", got: GPUSystems, want: 467},
		{name: "NVSwitch", got: NVSwitchSystems, want: 3072},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("mask = %#x, want %#x", test.got, test.want)
			}
		})
	}
	if GPUSystems&NVSwitchSystems != 0 || GPUSystems|NVSwitchSystems != KnownSystems {
		t.Fatal("entity-kind masks do not partition KnownSystems")
	}
}
