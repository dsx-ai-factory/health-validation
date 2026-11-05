// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

// TerminalRecoveryActions is the action each GPU's readable Xid 154
// reports leave it in, as recovery_action_counts derives it.
func TerminalRecoveryActions(events []XidEvent) map[string]RecoveryAction {
	reports := readableRecoveryReports(events)
	if len(reports) == 0 {
		return nil
	}
	terminal := make(map[string]RecoveryAction, len(reports))
	for gpu, evs := range reports {
		terminal[gpu], _ = latestRecoveryAction(evs)
	}
	return terminal
}
