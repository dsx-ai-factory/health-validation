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
	resolutions := NVLink5ResolutionCounts(m.Events, host.DriverVersion.Raw)
	if resolutions == nil {
		resolutions = make(map[string]int64)
	}
	return map[string]any{
		SignalSubXidCodeCounts:        subXidCodeCounts(m.Events),
		SignalRecoveryActionCounts:    recoveryActionCounts(m.Events),
		SignalNVLink5ResolutionCounts: resolutions,
	}
}
