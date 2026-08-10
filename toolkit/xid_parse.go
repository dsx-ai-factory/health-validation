package toolkit

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/NVIDIA/health-validation/catalog/xid"
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

// ParseXid scans lines for NVRM Xid entries and returns the aggregate
// measurement for the window described by since (e.g. "boot"). Lines
// that do not match are skipped. The returned measurement always has a
// non-nil meaning even with zero events: EventCount is 0 and XidCounts
// is nil, which a check reads as "no Xid faults observed".
func ParseXid(since string, lines []string) xid.XidMeasurement {
	m := xid.XidMeasurement{Since: since}
	for _, line := range lines {
		groups := xidLineRE.FindStringSubmatch(line)
		if groups == nil {
			continue
		}
		code, err := strconv.ParseInt(groups[2], 10, 64)
		if err != nil {
			// Code group is \d+, so this is unreachable in practice;
			// guard against an overflowing code rather than panicking.
			continue
		}

		ev := xid.XidEvent{PCIAddress: groups[1], XidCode: code}
		if groups[3] != "" {
			ev.Pid, _ = strconv.ParseInt(groups[3], 10, 64)
		}
		if groups[4] != "" {
			ev.ProcessName = strings.TrimSpace(groups[4])
		}

		m.Events = append(m.Events, ev)
		if m.XidCounts == nil {
			m.XidCounts = make(map[int64]int64)
		}
		m.XidCounts[code]++
		m.EventCount++
	}
	return m
}
