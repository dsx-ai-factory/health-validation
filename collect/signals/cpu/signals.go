// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package cpu owns the canonical definitions of host CPU topology signals.
package cpu

import "github.com/dsx-ai-factory/health-validation/collect"

// Node-scoped CPU topology counts, as the kernel reports them in sysfs.
var (
	PossibleCount       = collect.NewSignal("cpu.logical.possible.count", "1")
	OnlineCount         = collect.NewSignal("cpu.logical.online.count", "1")
	NUMANodeOnlineCount = collect.NewSignal("cpu.numa_node.online.count", "1")
)

// All returns every canonical CPU definition in stable order.
func All() collect.Signals {
	return collect.Signals{PossibleCount, OnlineCount, NUMANodeOnlineCount}
}
