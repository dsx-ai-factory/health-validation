// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package kmsg defines raw kernel-record signals.
package kmsg

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	Message  = collect.NewSignal("kernel.message", "")
	Priority = collect.NewSignal("kernel.priority", "1")
)
