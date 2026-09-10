package toolkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// linkFlapGrep is the journalctl --grep prefilter for mlx5 link
// transitions. It deliberately does not require an "rdma" token —
// the driver's netdev link lines do not carry one — so real outages
// are collected; the parser does the exact structuring.
const linkFlapGrep = `(?i)mlx5_core .* Link (up|down)`

// Defaults: a 1-hour lookback and no boot offset.
const (
	defaultLinkFlapLookback   = time.Hour
	defaultLinkFlapBootOffset = time.Duration(0)
)

// journalSinceLayout formats a time for journalctl --since. journalctl
// interprets an unqualified timestamp in the system's local time zone, so
// the value is formatted from a local time.
const journalSinceLayout = "2006-01-02 15:04:05"

// RDMALinkFlapBackend is a single data source for mlx5 RDMA link-flap
// events. The production backend reads the system journal; tests inject
// canned log lines via [MockRDMALinkFlap]. It returns one aggregate
// [infiniband.LinkFlapMeasurement] (even when no flaps were found)
// wrapped in [health.Measurement], so the catalog always has a
// measurement to evaluate.
type RDMALinkFlapBackend interface {
	Measure(ctx context.Context) ([]health.Measurement, error)
}

// RDMALinkFlapOption configures the journal-backed link-flap backend.
type RDMALinkFlapOption func(*rdmaLinkFlapBackend)

// WithLinkFlapLookback bounds the read to the last d of the current boot
// (default 1h): only outages within the window count, so a flap from
// early in a long-lived boot does not perpetually fail the check.
func WithLinkFlapLookback(d time.Duration) RDMALinkFlapOption {
	return func(b *rdmaLinkFlapBackend) {
		if d < 0 {
			b.configErr = errors.Join(b.configErr, fmt.Errorf("link-flap lookback must be non-negative, got %s", d))
			return
		}
		b.lookback = d
	}
}

// WithLinkFlapBootOffset ignores link events logged within d of boot.
// A host is unstable right after boot and the flaps it logs then are not
// actionable, so the effective start of the window is pushed forward by
// d. Default 0 (no offset).
func WithLinkFlapBootOffset(d time.Duration) RDMALinkFlapOption {
	return func(b *rdmaLinkFlapBackend) {
		if d < 0 {
			b.configErr = errors.Join(b.configErr, fmt.Errorf("link-flap boot offset must be non-negative, got %s", d))
			return
		}
		b.bootOffset = d
	}
}

// JournalctlRDMALinkFlap returns a backend that collects mlx5 RDMA link
// transitions from the current boot's kernel journal by running:
//
//	journalctl --dmesg --boot --no-pager --since=<start> --output=short-full --grep=<mlx5 link>
//
// --boot scopes the read to this boot and --since bounds it to the lookback
// window (default 1h); with a boot offset the window start is never earlier
// than boot + offset. --output=short-full is required (not --output=cat)
// because the parser needs the per-line timestamp to measure outage duration.
// Requires journalctl on PATH and permission to read the system journal. A
// read failure (or timeout) yields a single errored measurement rather than
// an error from Measure, so the catalog fails the link-flap check instead of
// dropping the host.
func JournalctlRDMALinkFlap(opts ...RDMALinkFlapOption) RDMALinkFlapBackend {
	b := &rdmaLinkFlapBackend{
		run:        runJournalctl,
		now:        time.Now,
		uptime:     readProcUptime,
		lookback:   defaultLinkFlapLookback,
		bootOffset: defaultLinkFlapBootOffset,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// rdmaLinkFlapBackend reads link lines from the journal. run, now, and uptime
// are seams so tests can drive the window and parsing without a host journal.
type rdmaLinkFlapBackend struct {
	// configErr accumulates invalid-option errors (e.g. a negative lookback
	// or boot offset) recorded at construction time; Measure surfaces it
	// without touching the journal.
	configErr  error
	run        runFunc
	now        func() time.Time
	uptime     func() (time.Duration, error)
	lookback   time.Duration
	bootOffset time.Duration
}

// Measure reads mlx5 link transitions in the lookback window. --output=short-full
// is required (not --output=cat) because the parser needs the per-line
// timestamp to measure outage duration. See [readJournal] for the clean-boot
// vs unreadable-journal handling.
func (b *rdmaLinkFlapBackend) Measure(ctx context.Context) ([]health.Measurement, error) {
	if b.configErr != nil {
		return []health.Measurement{
			infiniband.ErroredLinkFlap(sinceBoot, b.configErr, infiniband.MeasurementSignals...),
		}, nil
	}

	since := b.lookback
	if b.bootOffset > 0 {
		// Honoring the offset needs the boot time; without it the window would
		// silently include the unstable post-boot period, so a read failure here
		// is surfaced rather than swallowed.
		up, err := b.uptime()
		if err != nil {
			// Label with the lookback-window start like every other path (the
			// offset couldn't be applied); since is still the lookback here.
			return []health.Measurement{
				infiniband.ErroredLinkFlap(b.now().Add(-since).Format(journalSinceLayout), fmt.Errorf("reading /proc/uptime for boot offset: %w", err), infiniband.MeasurementSignals...),
			}, nil
		}
		// Start no earlier than boot + offset (clamped to now), and never before
		// the lookback window.
		if eff := up - b.bootOffset; eff < since {
			since = max(eff, 0)
		}
	}

	start := b.now().Add(-since).Format(journalSinceLayout)
	return readJournal(ctx, b.run, journalMatcher{
		args:    []string{"--since=" + start, "--output=short-full", "--grep=" + linkFlapGrep},
		subject: "link state",
		parse:   func(lines []string) health.Measurement { return ParseLinkFlaps(start, lines) },
		errored: func(err error) health.Measurement {
			return infiniband.ErroredLinkFlap(start, err, infiniband.MeasurementSignals...)
		},
	})
}

// readProcUptime returns the host's uptime from /proc/uptime.
func readProcUptime() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing /proc/uptime %q: %w", fields[0], err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// MockRDMALinkFlap returns a backend that parses the provided short-full
// log lines with the real parser, as if journalctl had returned them.
// Pass no lines to simulate a flap-free boot.
//
//	sensor := toolkit.NewRDMALinkFlapSensor(toolkit.MockRDMALinkFlap(
//	    "Thu 2025-12-11 11:49:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
//	    "Thu 2025-12-11 11:51:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
//	))
func MockRDMALinkFlap(lines ...string) RDMALinkFlapBackend {
	return &mockRDMALinkFlapBackend{lines: lines}
}

type mockRDMALinkFlapBackend struct {
	lines []string
}

func (m *mockRDMALinkFlapBackend) Measure(context.Context) ([]health.Measurement, error) {
	return []health.Measurement{ParseLinkFlaps(sinceBoot, m.lines)}, nil
}
