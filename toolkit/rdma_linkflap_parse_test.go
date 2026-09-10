package toolkit_test

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/toolkit"
)

var (
	onlyUpLines = []string{
		"Tue 2026-03-10 11:49:08 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
	}
	// One down→up outage of 1m54s = 114s.
	oneOutageLines = []string{
		"Wed 2025-12-10 11:49:08 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
		"Thu 2025-12-11 11:49:08 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:51:02 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
	}
	// Two outages: 11:49:00→11:51:00 (120s) and 11:53:00→11:54:00 (60s).
	twoOutageLines = []string{
		"Wed 2025-12-10 11:49:08 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
		"Thu 2025-12-11 11:49:00 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:51:00 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
		"Thu 2025-12-11 11:53:00 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:54:00 UTC node-name kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
	}
)

func TestParseLinkFlaps_Durations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  []int64
	}{
		{"no lines", nil, nil},
		{"only up", onlyUpLines, nil},
		{"one outage", oneOutageLines, []int64{114}},
		{"two outages", twoOutageLines, []int64{120, 60}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := toolkit.ParseLinkFlaps("boot", tc.lines)
			if !slices.Equal(m.OutageDurations, tc.want) {
				t.Errorf("OutageDurations = %v, want %v", m.OutageDurations, tc.want)
			}
			if len(m.FlapEvents) != len(tc.want) {
				t.Errorf("FlapEvents len = %d, want %d", len(m.FlapEvents), len(tc.want))
			}
		})
	}
}

// A down with no following up (an unrecovered link) is not paired — it
// records no outage.
func TestParseLinkFlaps_UnrecoveredDownIgnored(t *testing.T) {
	lines := []string{
		"Thu 2025-12-11 11:49:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
	}
	m := toolkit.ParseLinkFlaps("boot", lines)
	if len(m.OutageDurations) != 0 {
		t.Errorf("OutageDurations = %v, want none", m.OutageDurations)
	}
}

// Consecutive downs before an up (e.g. a reset re-emitting "down") are one
// continuous outage from the first down, not one per down. Each up recovers a
// single outage, so the second down does not re-pair with the same up.
func TestParseLinkFlaps_ConsecutiveDownsCollapse(t *testing.T) {
	lines := []string{
		"Thu 2025-12-11 11:49:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:49:30 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:51:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
	}
	m := toolkit.ParseLinkFlaps("boot", lines)
	// One outage, measured from the FIRST down (11:49:00) to the up (11:51:00).
	if !slices.Equal(m.OutageDurations, []int64{120}) {
		t.Errorf("OutageDurations = %v, want [120] (single outage from the first down)", m.OutageDurations)
	}
	if len(m.FlapEvents) == 1 && m.FlapEvents[0].DownAt != "2025-12-11 11:49:00" {
		t.Errorf("DownAt = %q, want the first down 2025-12-11 11:49:00", m.FlapEvents[0].DownAt)
	}
}

// Outages on different interfaces are tracked independently.
func TestParseLinkFlaps_PerInterface(t *testing.T) {
	lines := []string{
		"Thu 2025-12-11 11:49:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:49:40 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
		"Thu 2025-12-11 11:50:00 UTC host kernel: mlx5_core 0000:5b:00.0 enp91s0np0: Link down",
		"Thu 2025-12-11 11:50:05 UTC host kernel: mlx5_core 0000:5b:00.0 enp91s0np0: Link up",
	}
	m := toolkit.ParseLinkFlaps("boot", lines)
	// enp90 (40s) sorts before enp91 (5s): deterministic interface order.
	if !slices.Equal(m.OutageDurations, []int64{40, 5}) {
		t.Errorf("OutageDurations = %v, want [40 5]", m.OutageDurations)
	}
	if m.FlapEvents[0].Interface != "enp90s0np0" || m.FlapEvents[1].Interface != "enp91s0np0" {
		t.Errorf("interfaces = %q/%q", m.FlapEvents[0].Interface, m.FlapEvents[1].Interface)
	}
}

// Non-matching lines are skipped without error.
func TestParseLinkFlaps_IgnoresNoise(t *testing.T) {
	lines := []string{
		"Thu 2025-12-11 11:49:00 UTC host kernel: some other subsystem message",
		"garbage",
		"",
	}
	m := toolkit.ParseLinkFlaps("boot", lines)
	if len(m.OutageDurations) != 0 || m.Since != "boot" {
		t.Errorf("got %+v, want empty measurement with since=boot", m)
	}
}
