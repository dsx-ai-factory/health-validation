package toolkit

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// linkFlapLineRE matches an mlx5 RDMA interface link transition in a
// journalctl --output=short-full line, e.g.
//
//	Tue 2026-03-10 11:49:08 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up
//
// Group 1 is the timestamp, group 2 the interface (the token immediately
// before ": Link"), group 3 the up/down state. It does not require an
// "rdma" token — the netdev-style link lines the driver actually logs
// don't carry one; the parser stays permissive because the collector filters
// journal lines before parsing.
var linkFlapLineRE = regexp.MustCompile(`(?i)(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}).*?\bmlx5_core\b.*?(\S+): Link (up|down)\b`)

// linkFlapTimeLayout parses the short-full timestamp. The zone token that
// follows it in the log is ignored; every line in one journal shares a
// zone, so naive differences give correct outage durations.
const linkFlapTimeLayout = "2006-01-02 15:04:05"

// linkEvent is one parsed link transition on an interface.
type linkEvent struct {
	at   time.Time
	raw  string // the raw timestamp text, preserved for messages
	down bool
}

// ParseLinkFlaps scans short-full journal lines for mlx5 link
// transitions and returns the aggregate outage measurement for the
// window described by since (e.g. "boot"). Per interface it pairs each
// "Link down" with the next not-yet-consumed "Link up" and records that
// outage's length in seconds. Each up recovers one outage, so consecutive
// downs before an up (e.g. a reset re-emitting "down" with no intervening
// "up") collapse into a single outage from the first down; a down with no
// following up (an unrecovered link) is not paired. This deliberately
// avoids pairing every down with the same up, which would double-count
// one continuous outage. Lines that do not parse are skipped.
// The measurement always has meaning even with zero outages, which a check
// reads as "no fatal link flaps observed".
func ParseLinkFlaps(since string, lines []string) infiniband.LinkFlapMeasurement {
	byIface := make(map[string][]linkEvent)
	for _, line := range lines {
		g := linkFlapLineRE.FindStringSubmatch(line)
		if g == nil {
			continue
		}
		at, err := time.ParseInLocation(linkFlapTimeLayout, g[1], time.UTC)
		if err != nil {
			continue
		}
		iface := g[2]
		byIface[iface] = append(byIface[iface], linkEvent{
			at:   at,
			raw:  g[1],
			down: strings.EqualFold(g[3], "down"),
		})
	}

	m := infiniband.LinkFlapMeasurement{Since: since}
	ifaces := make([]string, 0, len(byIface))
	for iface := range byIface {
		ifaces = append(ifaces, iface)
	}
	sort.Strings(ifaces) // deterministic output order

	for _, iface := range ifaces {
		events := byIface[iface]
		sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
		// Each up recovers a single outage, so it may pair with only one down.
		// Without this, consecutive downs before an up (e.g. a reset re-emitting
		// "down" with no intervening "up") would each pair with that up and
		// record the one continuous outage several times.
		usedUp := make(map[int]bool)
		for i, ev := range events {
			if !ev.down {
				continue
			}
			for j := i + 1; j < len(events); j++ {
				if events[j].down || usedUp[j] {
					continue
				}
				usedUp[j] = true
				secs := int64(events[j].at.Sub(ev.at).Seconds())
				m.OutageDurations = append(m.OutageDurations, secs)
				m.FlapEvents = append(m.FlapEvents, infiniband.LinkFlapEvent{
					Interface:       iface,
					DownAt:          ev.raw,
					UpAt:            events[j].raw,
					DurationSeconds: secs,
				})
				break
			}
		}
	}
	return m
}
