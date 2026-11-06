// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"
	"slices"
)

// readableRecoveryReports groups the Xid 154 events whose transition was
// read by GPU.
func readableRecoveryReports(events []XidEvent) map[string][]XidEvent {
	reports := make(map[string][]XidEvent)
	for _, ev := range events {
		if ev.XidCode == 154 && ev.RecoveryAction != nil {
			reports[ev.PCIAddress] = append(reports[ev.PCIAddress], ev)
		}
	}
	return reports
}

// latestRecoveryAction picks the action one GPU's readable Xid 154
// reports leave it in.
//
// An Xid 154 reports a transition, not a state, and a GPU can escalate
// through several (None -> Drain and Reset -> GPU Reset Required), so the
// To of its final transition is the latest action the journal shows.
// "Final" is by Sequence, the report's position in the journal scan, not
// by its place in events: a source may hand events over out of order.
// When the reports do not all carry a distinct Sequence, the order is
// unknown, so the GPU keeps the most disruptive action it reported, and a
// None cannot clear a request that may have come after it. ordered
// reports which applied.
//
// It is not guaranteed to be the action in effect now. The driver logs no
// 154 when a GPU is reinitialized, as after a reset, and suppresses it for
// some transitions it clears itself, so a GPU that was reset still shows
// its last requested action until the journal window moves past it.
func latestRecoveryAction(reports []XidEvent) (action RecoveryAction, ordered bool) {
	ordered = true
	seen := make(map[int64]bool, len(reports))
	for _, ev := range reports {
		if ev.Sequence <= 0 || seen[ev.Sequence] {
			ordered = false
			break
		}
		seen[ev.Sequence] = true
	}
	best := reports[0]
	for _, ev := range reports[1:] {
		if ordered {
			if ev.Sequence > best.Sequence {
				best = ev
			}
		} else if recoveryActionRank(*ev.RecoveryAction) > recoveryActionRank(*best.RecoveryAction) {
			best = ev
		}
	}
	return *best.RecoveryAction, ordered
}

// recoveryActionRank orders recovery actions by how disruptive their
// remediation is, following [recoverySteps], so an unordered GPU keeps the
// one that must not be missed. An unmapped code ranks above None: it is a
// request this catalog cannot name, not the absence of one.
func recoveryActionRank(action RecoveryAction) int {
	code, ok := parseHexUint32(action.ToCode)
	if !ok {
		return 1
	}
	if code == 0x0 {
		return 0
	}
	for i, step := range recoverySteps {
		if slices.Contains(step.codes, code) {
			return len(recoverySteps) + 1 - i
		}
	}
	return 1
}

// canonicalRecoveryActionCode renders a recovery-action code one way, so
// the same action keys the same signal entry however NVRM spelled it. An
// unparseable value passes through rather than being dropped, keeping a
// malformed line visible instead of silently absent.
func canonicalRecoveryActionCode(raw string) string {
	v, ok := parseHexUint32(raw)
	if !ok {
		return raw
	}
	return fmt.Sprintf("0x%x", v)
}

// recoveryActionCounts is the recovery_action_counts signal: for each
// recovery action, how many Xid 154 reports ask for it as a GPU's current
// action.
//
// The value is keyed by the action code rather than its name — the code
// is what NVRM commits to, while the name is display text — canonicalised
// so a padded or upper-case rendering keys the same entry.
//
// A report counts only toward its GPU's current action (see
// [latestRecoveryAction]): a GPU that escalated None -> Drain and
// Reset -> GPU Reset Required counts once, under GPU reset, not under
// actions it has since left. When a GPU's order is unknown, every report
// counts under its own action, so an uncertain request is never dropped.
// Each check rule compares one action's count with occurrences, so the
// threshold asks how often the driver requested that action, not how many
// Xid 154 lines the window held across every GPU.
//
// This reports which actions the driver asked for, not which fault
// asked for them. Joining an individual XID_154_EVAL fault to its GPU's
// action still needs the decode table, and stays with the decode.
//
// Every Xid 154 whose transition could not be read counts under
// [recoveryActionUnreadable], whether or not its GPU also logged a
// readable one, so an unreadable line is never silently dropped.
func recoveryActionCounts(events []XidEvent) map[string]int64 {
	counts := make(map[string]int64)
	for _, reports := range readableRecoveryReports(events) {
		current, ordered := latestRecoveryAction(reports)
		currentCode := canonicalRecoveryActionCode(current.ToCode)
		for _, ev := range reports {
			code := canonicalRecoveryActionCode(ev.RecoveryAction.ToCode)
			if !ordered || code == currentCode {
				counts[code]++
			}
		}
	}
	for _, ev := range events {
		if ev.XidCode == 154 && ev.RecoveryAction == nil {
			counts[recoveryActionUnreadable]++
		}
	}
	return counts
}

// recoveryActionUnreadable is the recovery_action_counts key for Xid 154
// reports whose transition could not be read. It cannot
// collide with a code, which is always read as a hex value.
const recoveryActionUnreadable = "unreadable"
