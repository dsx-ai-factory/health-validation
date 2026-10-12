// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pci defines raw PCI inventory and Access Control Services signals.
package pci

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	Address       = collect.NewSignal("pci.address", "")
	Vendor        = collect.NewSignal("pci.vendor.id", "1")
	Device        = collect.NewSignal("pci.device.id", "1")
	Class         = collect.NewSignal("pci.class.code", "1")
	Driver        = collect.NewSignal("pci.driver.name", "")
	Module        = collect.NewSignal("pci.driver.module", "")
	GPUPresent    = collect.NewSignal("pci.gpu.present", "")
	ACSCapability = collect.NewSignal("pci.acs.capability.bits", "1")
	ACSControl    = collect.NewSignal("pci.acs.control.bits", "1")
	ACSOffset     = collect.NewSignal("pci.acs.config.offset", "By")
)

// All returns the definitions in stable order. Module names identify only the
// bound driver module, not the candidate modules from a userspace alias database.
func All() collect.Signals {
	return collect.Signals{Address, Vendor, Device, Class, Driver, Module, GPUPresent, ACSCapability, ACSControl, ACSOffset}
}
