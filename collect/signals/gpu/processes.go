// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	ProcessCount      = collect.NewSignal("gpu.process.count", "1")
	ProcessMember     = collect.NewSignal("gpu.process.compute.member", "")
	ProcessMemoryUsed = collect.NewSignal("gpu.process.memory.used", "bytes")
	ProcessName       = collect.NewSignal("gpu.process.name", "")
	ProcessState      = collect.NewSignal("gpu.process.state", "")
)

func Processes() collect.Signals {
	return collect.Signals{ProcessCount, ProcessMember, ProcessMemoryUsed, ProcessName, ProcessState}
}

// ProcessEntitySignals excludes the GPU-level count. An empty entity set can
// report NO_TARGETS at its GPU parent without inventing a process identity.
func ProcessEntitySignals() collect.Signals {
	return collect.Signals{ProcessMember, ProcessMemoryUsed, ProcessName, ProcessState, ProcessCmdline, ProcessCmdlineTruncated}
}

// Process diagnostics are declared only by explicitly configured sensors.
var (
	ProcessCmdline          = collect.NewSignal("gpu.process.cmdline", "")
	ProcessCmdlineTruncated = collect.NewSignal("gpu.process.cmdline.truncated", "")
)

func ProcessDiagnostics() collect.Signals {
	return collect.Signals{ProcessCmdline, ProcessCmdlineTruncated}
}
