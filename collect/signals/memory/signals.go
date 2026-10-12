// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package memory owns canonical host memory accounting signals.
package memory

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	TotalBytes     = collect.NewSignal("memory.total.bytes", "By")
	FreeBytes      = collect.NewSignal("memory.free.bytes", "By")
	AvailableBytes = collect.NewSignal("memory.available.bytes", "By")
	// UsedBytes is total minus available, including unreclaimable kernel memory.
	UsedBytes         = collect.NewSignal("memory.used.bytes", "By")
	VmallocTotalBytes = collect.NewSignal("memory.vmalloc.total.bytes", "By")
	VmallocUsedBytes  = collect.NewSignal("memory.vmalloc.used.bytes", "By")
	BPFJITBytes       = collect.NewSignal("memory.bpf_jit.bytes", "By")
)

func All() collect.Signals {
	return collect.Signals{TotalBytes, FreeBytes, AvailableBytes, UsedBytes, VmallocTotalBytes, VmallocUsedBytes, BPFJITBytes}
}
