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
//	NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000 ...)
//	NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02
//
// Unlike other Xid codes, these carry a human-readable sub-component
// name and a literal Fatal/Nonfatal severity instead of pid/name.
// Group 1 PCI address, 2 Xid code, 3 sub-component, 4 Fatal|Nonfatal,
// 5 crosscontain digit, 6 injected digit, 7 link number, 8 the
// optional parenthesized intrInfo/errorStatus/errorDebugData tail
// (raw — split on whitespace by the caller rather than matched
// field-by-field, since the trailing debug-data register count is not
// confirmed to be fixed).
var xidNVLink5RE = regexp.MustCompile(`NVRM: Xid \(PCI:([0-9a-fA-F:.]+)\): (14[4-9]|150), (\S+) (Fatal|Nonfatal) XC(\d+) i(\d+) Link (\d+)(?: \(([^)]*)\))?`)

// ParseXid scans lines for NVRM Xid entries and returns the aggregate
// measurement for the window described by since (e.g. "boot"). Lines
// that do not match are skipped. The returned measurement always has a
// non-nil meaning even with zero events: EventCount is 0 and XidCounts
// is nil, which a check reads as "no Xid faults observed".
//
// Every field this parses comes straight from the log line (or, for
// SubXidCode, a fixed bit-mask over a field the log line carries) — no
// NVIDIA decode-table lookup happens here.
func ParseXid(since string, lines []string) xid.XidMeasurement {
	m := xid.XidMeasurement{Since: since}
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
	link, _ := strconv.ParseInt(groups[7], 10, 64)

	detail := &xid.NVLink5Detail{
		SubComponent: groups[3],
		Fatal:        groups[4] == "Fatal",
		Link:         link,
	}
	if tail := strings.Fields(groups[8]); len(tail) > 0 {
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

	return xid.XidEvent{PCIAddress: groups[1], XidCode: code, NVLink5: detail}, true
}
