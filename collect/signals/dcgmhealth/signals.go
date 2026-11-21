// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgmhealth defines the DCGM passive-health report signal and masks.
// Reports retain native findings without assigning Health Validation verdicts.
package dcgmhealth

import "github.com/dsx-ai-factory/health-validation/collect"

// Native DCGM health-system masks. KnownSystems covers the supported selection;
// SystemAll is DCGM's group-wide incident value, not that union.
const (
	SystemPCIe    uint64 = 0x1
	SystemNVLink  uint64 = 0x2
	SystemMemory  uint64 = 0x10
	SystemInfoROM uint64 = 0x40
	SystemThermal uint64 = 0x80
	SystemPower   uint64 = 0x100
	// SystemDriver monitors GPU recovery actions reported by the driver.
	SystemDriver           uint64 = 0x200
	SystemNVSwitchNonfatal uint64 = 0x400
	SystemNVSwitchFatal    uint64 = 0x800
	KnownSystems                  = SystemPCIe | SystemNVLink | SystemMemory | SystemInfoROM | SystemThermal | SystemPower | SystemDriver | SystemNVSwitchNonfatal | SystemNVSwitchFatal
	SystemAll              uint64 = 0xffffffff
	NVSwitchSystems               = SystemNVSwitchFatal | SystemNVSwitchNonfatal
	GPUSystems                    = KnownSystems &^ NVSwitchSystems
)

// Report carries the complete result of one DCGM passive-health read.
var Report = collect.NewSignal("dcgm.health", "")

// All returns the aggregate passive-health report signal.
func All() collect.Signals {
	return collect.Signals{Report}
}
