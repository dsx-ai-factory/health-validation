// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kernel defines kernel-record signals for common sensor use.
package kernel

import "github.com/dsx-ai-factory/health-validation/collect"

// Message is the unmodified record body; Priority includes facility and level.
var (
	Message  = collect.NewSignal("kernel.message", "")
	Priority = collect.NewSignal("kernel.priority", "1")
	// Fault signals carry the exact matched regex, not a health verdict.
	Panic    = collect.NewSignal("kernel.panic", "")
	OOM      = collect.NewSignal("kernel.oom", "")
	HungTask = collect.NewSignal("kernel.hung_task", "")
	Lockup   = collect.NewSignal("kernel.lockup", "")
	FileMax  = collect.NewSignal("kernel.file_max", "")
)

func All() collect.Signals {
	return collect.Signals{Message, Priority, Panic, OOM, HungTask, Lockup, FileMax}
}
