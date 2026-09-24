// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import "github.com/dsx-ai-factory/health-validation/catalog/health"

var _ health.DerivedSignaler = XidMeasurement{}

// DerivedSignals implements [health.DerivedSignaler]. These signals are
// computed from Events rather than collected, so any sensor that yields
// Xid events gets them interpreted the same way instead of each
// reimplementing the NVIDIA-specific parts.
func (m XidMeasurement) DerivedSignals(host health.Host) map[string]any {
	// Derived once and passed to both signals that need it: resolving a
	// fault needs the same per-GPU actions, and two independent
	// derivations of one thing can drift apart.
	terminal := TerminalRecoveryActions(m.Events)
	resolutions := nvlink5ResolutionCounts(m.Events, host.DriverVersion, terminal)
	if resolutions == nil {
		resolutions = make(map[string]int64)
	}
	return map[string]any{
		SignalSubXidCodeCounts:        subXidCodeCounts(m.Events),
		SignalRecoveryActionCounts:    recoveryActionCounts(terminal),
		SignalNVLink5ResolutionCounts: resolutions,
	}
}
