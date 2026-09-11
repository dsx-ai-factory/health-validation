package xid

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// xidLineRE matches an NVRM Xid log line, e.g.
//
//	NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3
//
// It is unanchored so a journalctl/syslog prefix on the line does not
// prevent a match. Group 1 is the PCI address, group 2 the Xid code
// (required); groups 3 and 4 are the optional pid and process name —
// some Xid lines (e.g. channel/interrupt dumps) carry neither.
var xidLineRE = regexp.MustCompile(`NVRM: Xid \(PCI:([0-9a-fA-F:.]+)\): (\d+)(?:,\s*pid=(\d+))?(?:,\s*name=([^,]+))?`)

// xidNVLink5RE matches the extended log format NVRM uses for the
// NVLink5/NVSwitch fault family (Xid 144-150, Blackwell-only), e.g.
//
//	NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT  Fatal   XC0 i0 Link 17 (0x004001C6 0x00000000 ...)
//	NVRM: Xid (PCI:0009:01:00): 145, pid=2348952, name=cache_mgr_main, RLW_SRC_TRACK Fatal XC1 i0 Link 17 (0x040a28e2 0x00000020 ...)
//	NVRM: Xid (PCI:0009:01:00): 149, NETIR  Fatal   XC0 i0 Link -1 (0x000fe406 0x00000000 ...)
//	NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02
//
// These carry a human-readable sub-component name and a literal
// Fatal/Nonfatal severity that other Xid codes do not. Three real-world
// variations the format permits, each of which a stricter pattern
// silently drops to [xidLineRE] — losing the sub-component, severity,
// and registers the catalog decodes from:
//
//   - pid= and name= may precede the sub-component. They are not
//     mutually exclusive with it, despite carrying the same meaning
//     they do for every other Xid code.
//   - Fields are separated by runs of whitespace, not single spaces.
//   - Link is -1 when the fault is not attributable to one link.
//
// Group 1 PCI address, 2 Xid code, 3 pid and 4 process name (both
// optional), 5 sub-component, 6 Fatal|Nonfatal, 7 crosscontain digit,
// 8 injected digit, 9 link number, 10 the optional parenthesized
// intrInfo/errorStatus/errorDebugData tail (raw — split on whitespace
// by the caller rather than matched field-by-field, since the trailing
// debug-data register count is not confirmed to be fixed).
var xidNVLink5RE = regexp.MustCompile(
	`NVRM: Xid \(PCI:([0-9a-fA-F:.]+)\): (14[4-9]|150),\s*` +
		`(?:pid=(\d+),\s*)?(?:name=([^,]+),\s*)?` +
		`(\S+)\s+(Fatal|Nonfatal)\s+XC(\d+)\s+i(\d+)\s+Link\s+(-?\d+)` +
		`(?:\s+\(([^)]*)\))?`)

// ParseXid scans lines for NVRM Xid entries and returns the aggregate
// measurement for the window described by since (e.g. "boot"). Lines
// that do not match are skipped. The returned measurement always has a
// non-nil meaning even with zero events: EventCount is 0 and XidCounts
// is nil, which a check reads as "no Xid faults observed".
//
// Every field this parses comes straight from the log line (or, for
// SubXidCode, a fixed bit-mask over a field the log line carries) — no
// NVIDIA decode-table lookup happens here. driverVersion is the raw
// host driver version string (see [DetectDriverVersion]), stamped
// as-is onto the measurement so the catalog can classify it and decode
// the NVLink5 family's resolution bucket itself (see
// catalog/xid.DecodeNVLink5Resolution). It is variadic so existing
// callers are unaffected; only the first value is used, and passing
// more than one is a caller error and the rest are ignored.
func ParseXid(since string, lines []string, driverVersion ...string) xid.XidMeasurement {
	m := xid.XidMeasurement{Since: since}
	if len(driverVersion) > 0 {
		m.DriverVersion = driverVersion[0]
	}
	for _, line := range lines {
		ev, ok := parseNVLink5Line(line)
		if !ok {
			ev, ok = parseXidLine(line)
		}
		if !ok {
			continue
		}

		m.Events = append(m.Events, ev)
		if m.XidCounts == nil {
			m.XidCounts = make(map[int64]int64)
		}
		m.XidCounts[ev.XidCode]++
		m.EventCount++
		if ev.NVLink5 != nil && ev.NVLink5.Fatal {
			if m.FatalXidCounts == nil {
				m.FatalXidCounts = make(map[int64]int64)
			}
			m.FatalXidCounts[ev.XidCode]++
		}
		if ev.NVLink5 != nil && ev.NVLink5.SubXidCode != "" {
			if m.SubXidCodeCounts == nil {
				m.SubXidCodeCounts = make(map[string]int64)
			}
			m.SubXidCodeCounts[ev.NVLink5.SubXidCode]++
		}
	}
	return m
}

// parseXidLine matches the standard "pid=/name=" Xid line format used
// by every code outside the NVLink5 family.
func parseXidLine(line string) (xid.XidEvent, bool) {
	groups := xidLineRE.FindStringSubmatch(line)
	if groups == nil {
		return xid.XidEvent{}, false
	}
	code, err := strconv.ParseInt(groups[2], 10, 64)
	if err != nil {
		// Code group is \d+, so this is unreachable in practice; guard
		// against an overflowing code rather than panicking.
		return xid.XidEvent{}, false
	}

	ev := xid.XidEvent{PCIAddress: groups[1], XidCode: code}
	if groups[3] != "" {
		ev.Pid, _ = strconv.ParseInt(groups[3], 10, 64)
	}
	if groups[4] != "" {
		ev.ProcessName = strings.TrimSpace(groups[4])
	}
	return ev, true
}

// parseNVLink5Line matches the extended sub-component/severity format
// used by the NVLink5 fault family (Xid 144-150).
func parseNVLink5Line(line string) (xid.XidEvent, bool) {
	groups := xidNVLink5RE.FindStringSubmatch(line)
	if groups == nil {
		return xid.XidEvent{}, false
	}
	code, err := strconv.ParseInt(groups[2], 10, 64)
	if err != nil {
		return xid.XidEvent{}, false
	}
	link, _ := strconv.ParseInt(groups[9], 10, 64)

	detail := &xid.NVLink5Detail{
		SubComponent: groups[5],
		Fatal:        groups[6] == "Fatal",
		Link:         link,
	}
	if tail := strings.Fields(groups[10]); len(tail) > 0 {
		detail.IntrInfo = tail[0]
		if len(tail) > 1 {
			detail.ErrorStatus = tail[1]
		}
		if len(tail) > 2 {
			detail.DebugData = tail[2:]
		}
	}
	if subXidCode, ok := nvlink5SubXidCode(code, detail.IntrInfo); ok {
		detail.SubXidCode = subXidCode
	}

	// pid/name are optional here but carry the same meaning as on any
	// other Xid line, so they populate the same event fields.
	pid, _ := strconv.ParseInt(groups[3], 10, 64)

	return xid.XidEvent{
		PCIAddress:  groups[1],
		XidCode:     code,
		Pid:         pid,
		ProcessName: groups[4],
		NVLink5:     detail,
	}, true
}
