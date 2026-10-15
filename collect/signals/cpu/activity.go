// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cpu

import "github.com/dsx-ai-factory/health-validation/collect"

// Times are cumulative across all logical CPUs, not utilization percentages.
// Guest time is included in user time; guest-nice time is included in nice time.
var (
	UserSeconds      = collect.NewSignal("cpu.time.user.seconds", "s")
	NiceSeconds      = collect.NewSignal("cpu.time.nice.seconds", "s")
	SystemSeconds    = collect.NewSignal("cpu.time.system.seconds", "s")
	IdleSeconds      = collect.NewSignal("cpu.time.idle.seconds", "s")
	IOWaitSeconds    = collect.NewSignal("cpu.time.iowait.seconds", "s")
	IRQSeconds       = collect.NewSignal("cpu.time.irq.seconds", "s")
	SoftIRQSeconds   = collect.NewSignal("cpu.time.softirq.seconds", "s")
	StealSeconds     = collect.NewSignal("cpu.time.steal.seconds", "s")
	GuestSeconds     = collect.NewSignal("cpu.time.guest.seconds", "s")
	GuestNiceSeconds = collect.NewSignal("cpu.time.guest_nice.seconds", "s")
	Load1            = collect.NewSignal("cpu.load.1min", "1")
	Load5            = collect.NewSignal("cpu.load.5min", "1")
	Load15           = collect.NewSignal("cpu.load.15min", "1")
)

// Activity returns cumulative time and load definitions in stable order.
func Activity() collect.Signals {
	return collect.Signals{
		UserSeconds, NiceSeconds, SystemSeconds, IdleSeconds, IOWaitSeconds,
		IRQSeconds, SoftIRQSeconds, StealSeconds, GuestSeconds, GuestNiceSeconds, Load1, Load5, Load15,
	}
}
