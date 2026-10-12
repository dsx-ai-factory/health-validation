// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package host owns canonical host resource accounting signals.
package host

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	// File handles are kernel file descriptions, not per-process descriptors.
	FileHandlesAllocated = collect.NewSignal("host.file_handle.allocated.count", "1")
	FileHandlesUnused    = collect.NewSignal("host.file_handle.unused.count", "1")
	FileHandlesMax       = collect.NewSignal("host.file_handle.max.count", "1")
	// OpenFileDescriptors sums descriptor table entries for visible processes.
	// Shared file descriptions can have multiple descriptor table entries.
	OpenFileDescriptors = collect.NewSignal("host.open_file_descriptor.count", "1")
	ProcessesCreated    = collect.NewSignal("host.process.created.count", "1")
	RunnableTasks       = collect.NewSignal("host.task.runnable.count", "1")
	BlockedTasks        = collect.NewSignal("host.task.blocked.count", "1")
)

func All() collect.Signals {
	return collect.Signals{
		FileHandlesAllocated, FileHandlesUnused, FileHandlesMax, OpenFileDescriptors,
		ProcessesCreated, RunnableTasks, BlockedTasks,
	}
}
