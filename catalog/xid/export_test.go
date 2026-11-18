// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// The resolution machinery is unexported; these give the black-box tests
// in package xid_test access to it.

var NVLink5ResolutionKey = nvlink5ResolutionKey

func NVLink5ResolutionCounts(events []XidEvent, driverVersion health.DriverVersion) map[string]int64 {
	return nvlink5ResolutionCounts(events, "", driverVersion)
}

// TerminalRecoveryActions is the action each GPU's readable Xid 154
// reports leave it in, as recovery_action_counts derives it.
func TerminalRecoveryActions(events []XidEvent) map[string]RecoveryAction {
	reports := readableRecoveryReports(events, "")
	if len(reports) == 0 {
		return nil
	}
	terminal := make(map[string]RecoveryAction, len(reports))
	for gpu, evs := range reports {
		terminal[gpu], _ = latestRecoveryAction(evs)
	}
	return terminal
}

const (
	ResolutionIgnore              = resolutionIgnore
	ResolutionResetGPU            = resolutionResetGPU
	ResolutionResetGPUUnconfirmed = resolutionResetGPUUnconfirmed
	ResolutionUndecoded           = resolutionUndecoded
	ResolutionXid154Eval          = resolutionXid154Eval
	ResolutionXid154Missing       = resolutionXid154Missing
)
