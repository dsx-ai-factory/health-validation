// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import "github.com/dsx-ai-factory/health-validation/collect"

// GraphicsClock is distinct from the SM clock even when their values coincide.
var (
	GraphicsClock       = collect.NewSignal("gpu.clock.graphics", "megahertz")
	FramebufferReserved = collect.NewSignal("gpu.memory.frame_buffer.reserved", "mebibytes")
	UnifiedMemory       = collect.NewSignal("gpu.memory.unified", "")
	PersistenceMode     = collect.NewSignal("gpu.persistence_mode", "1")
	MinorNumber         = collect.NewSignal("gpu.minor_number", "1")
)

func TelemetrySignals() collect.Signals {
	return collect.Signals{GraphicsClock, FramebufferReserved, UnifiedMemory, PersistenceMode, MinorNumber}
}
