// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"fmt"
	"strconv"
	"strings"
)

// nvlink5SubXidCodeMask isolates IntrInfo[25:20], the 6-bit fault-subtype
// field NVRM packs into the IntrInfo register it logs for the NVLink5/
// NVSwitch fault family (Xid 144-150). Both driver eras place it here.
// Not to be confused with the decode-table guide's "Subcode", a
// bit-encoding of SubComponent that repeats text NVRM already prints.
const nvlink5SubXidCodeMask = 0x3F00000

// SubXidCode identifies which fault subtype within its Xid code an
// NVLink5-family event is, formatted "<code>.<hex digit>" (e.g. "149.4",
// "149.A") from IntrInfo[25:20]. Returns ok=false for an event with no
// NVLink5 detail or an IntrInfo that does not parse.
//
// Derived here rather than by whatever collected the event, so every
// source of Xid events keys subtypes identically, and the digit sits
// beside the decode table that says which digits name one fault and
// which are shared (see [nvlink5SubXidCodeResolvesAlike]).
func (e XidEvent) SubXidCode() (string, bool) {
	if e.NVLink5 == nil {
		return "", false
	}
	v, ok := parseHexUint32(e.NVLink5.IntrInfo)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d.%X", e.XidCode, (v&nvlink5SubXidCodeMask)>>20), true
}

// subXidCodeCounts tallies events by [XidEvent.SubXidCode], counting only
// events whose sub-component has per-subcode checks. A digit names a
// subtype only within its sub-component: the same bits under another,
// such as a bit-error event, are a different fault.
func subXidCodeCounts(events []XidEvent) map[string]int64 {
	counts := make(map[string]int64)
	for _, ev := range events {
		code, ok := ev.SubXidCode()
		if !ok || !hasSubXidCodeChecks(ev.XidCode, ev.NVLink5.SubComponent) {
			continue
		}
		counts[code]++
	}
	return counts
}

// hasSubXidCodeChecks reports whether subComponent under code is a
// decode-table group [nvlink5SubXidCodes] builds per-subcode checks for.
func hasSubXidCodeChecks(code int64, subComponent string) bool {
	prefix := fmt.Sprintf("%d.", code)
	for _, c := range nvlink5SubXidCodes {
		if strings.HasPrefix(c.subXidCode, prefix) && subcodeMatches(subComponent, c.subcode) {
			return true
		}
	}
	return false
}

// parseHexUint32 parses a hex string, with or without a "0x" prefix,
// into a uint32.
func parseHexUint32(s string) (uint32, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// subcodeMatches reports whether the literal subcode name NVRM printed
// in the log line as SubComponent (log) matches one of the
// "/"-separated alternative names in a decode-table row's subcode
// (table), e.g. table "TLW_RX/TLW_RX_PIPE0" matches log
// "TLW_RX_PIPE0". Comparison normalizes spaces to underscores and is
// case-insensitive, since the source spreadsheet uses "MSE Degraded"
// where a log line would print an underscore-joined identifier.
func subcodeMatches(log, table string) bool {
	normalize := func(s string) string {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
	}
	log = normalize(log)
	for _, alt := range strings.Split(table, "/") {
		if normalize(alt) == log {
			return true
		}
	}
	return false
}
