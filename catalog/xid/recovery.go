// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import "strconv"

// recoveryCode keys on the printed numbers, never the driver-version-dependent
// names. readable reports whether the transition parsed; a readable code
// outside nvmlDeviceGpuRecoveryAction_t's guided values is "unknown".
func recoveryCode(event XidEvent) (code string, readable bool) {
	r := event.RecoveryAction
	if r == nil || r.FromName == "" || r.ToName == "" {
		return "unknown", false
	}
	if _, err := strconv.ParseUint(r.FromCode, 0, 32); err != nil {
		return "unknown", false
	}
	to, err := strconv.ParseUint(r.ToCode, 0, 32)
	if err != nil {
		return "unknown", false
	}
	switch to {
	case 0, 1, 2, 3, 4, 6, 7:
		return strconv.FormatUint(to, 10), true
	}
	return "unknown", true
}

// recoveryActionCounts counts Xid 154 reports per guidance key. Sequence
// orders events within one source scan/measurement only. Each device
// keeps only its latest readable report; without positive, unique sequence
// evidence, every readable report is retained, so an unordered None never
// retires an actionable request. Unreadable reports are always counted as
// "unknown" and never replace a readable request: a line that cannot be
// parsed says nothing about which action the driver now requires.
func recoveryActionCounts(events []XidEvent) map[string]int64 {
	type state struct {
		code    string
		latest  int64
		ordered bool
	}
	type position struct {
		device   string
		sequence int64
	}
	devices := make(map[string]*state)
	seen := make(map[position]bool)
	for i := range events {
		event := &events[i]
		code, readable := recoveryCode(*event)
		if event.XidCode != 154 || !readable {
			continue
		}
		current := devices[event.PCIAddress]
		if current == nil {
			current = &state{code: code, latest: event.Sequence, ordered: event.PCIAddress != ""}
			devices[event.PCIAddress] = current
		} else if event.Sequence > current.latest {
			current.code, current.latest = code, event.Sequence
		}
		key := position{device: event.PCIAddress, sequence: event.Sequence}
		if event.Sequence <= 0 || seen[key] {
			current.ordered = false
		}
		seen[key] = true
	}
	counts := make(map[string]int64)
	for i := range events {
		event := &events[i]
		if event.XidCode != 154 {
			continue
		}
		code, readable := recoveryCode(*event)
		current := devices[event.PCIAddress]
		if !readable || !current.ordered || code == current.code {
			counts[code]++
		}
	}
	return counts
}
