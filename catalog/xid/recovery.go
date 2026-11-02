// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import "strconv"

// recoveryCode keys on the printed numbers, never the driver-version-dependent names.
func recoveryCode(event XidEvent) string {
	r := event.RecoveryAction
	if r == nil || r.FromName == "" || r.ToName == "" {
		return "unknown"
	}
	if _, err := strconv.ParseUint(r.FromCode, 0, 32); err != nil {
		return "unknown"
	}
	code, err := strconv.ParseUint(r.ToCode, 0, 32)
	if err != nil || code > 4 {
		return "unknown"
	}
	return strconv.FormatUint(code, 10)
}

// Sequence orders events within one source scan/measurement only. Without
// positive, unique sequence evidence, retain every report for that device:
// an unordered None must never retire an actionable recovery request.
func recoveryActionCounts(events []XidEvent) map[string]int64 {
	type state struct {
		code    string
		latest  int
		ordered bool
	}
	type position struct {
		device   string
		sequence int64
	}
	devices := make(map[string]state)
	seen := make(map[position]bool)
	for i := range events {
		event := &events[i]
		if event.XidCode != 154 {
			continue
		}
		current, ok := devices[event.PCIAddress]
		if !ok {
			current = state{code: recoveryCode(*event), latest: i, ordered: event.PCIAddress != ""}
		}
		key := position{device: event.PCIAddress, sequence: event.Sequence}
		if event.Sequence <= 0 || seen[key] {
			current.ordered = false
		}
		seen[key] = true
		if event.Sequence > events[current.latest].Sequence {
			current.latest = i
			current.code = recoveryCode(*event)
		}
		devices[event.PCIAddress] = current
	}
	counts := make(map[string]int64)
	for i := range events {
		event := &events[i]
		if event.XidCode != 154 {
			continue
		}
		current := devices[event.PCIAddress]
		code := recoveryCode(*event)
		if !current.ordered || code == current.code {
			counts[code]++
		}
	}
	return counts
}
