package toolkit

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	xidparse "github.com/dsx-ai-factory/health-validation/toolkit/xid"
)

// XidBackend is a single data source for NVRM Xid events. The concrete
// production backend reads the system journal; tests inject canned log
// lines via [MockXid]. It returns one aggregate [xid.XidMeasurement]
// (even when no events were found) wrapped in [health.Measurement], so
// the catalog always has a measurement to evaluate.
//
// Measure honors the caller's context: the journal read is a cancellable
// command, so Collect's per-sensor watchdog bounds it directly.
type XidBackend interface {
	Measure(ctx context.Context) ([]health.Measurement, error)
}

// Design note — alternative backend: the kernel ring buffer (/dev/kmsg).
//
// Xid events are kernel NVRM messages, so a backend could read /dev/kmsg
// directly (the same source dmesg and journald ingest from) instead of
// shelling out to journalctl. That is appealing for the container story:
// no external journalctl binary to bundle, no journal-format/version
// coupling between the image and the host, and no journal mounts to wire —
// the image would be fully self-contained.
//
// The blocker is retention. /dev/kmsg is a bounded ring buffer
// (CONFIG_LOG_BUF_SHIFT, typically a few thousand lines) that wraps under
// log pressure, so an Xid logged early on a long-lived boot can be evicted
// before the check runs. The systemd journal persists entries for the whole
// boot, which is what today's "retention since boot" expectation relies on
// (the checks are scoped to --boot). Switching to kmsg trades that
// guarantee for a best-effort recent window.
//
// Not adopted pending socialization and a decision on whether the
// since-boot retention constraint can be relaxed (or the ring-buffer bound
// accepted). If it can, a kmsg backend is a clean simplification here.

// Journalctl returns a backend that collects NVRM Xid events from the
// current boot's kernel journal by running:
//
//	journalctl --dmesg --boot --no-pager --output=cat --grep=Xid
//
// Requires journalctl on PATH and permission to read the system
// journal. A read failure (or timeout) yields a single errored
// [xid.XidMeasurement] rather than an error from Measure, so the
// catalog fails the applicable Xid checks instead of dropping the host.
func Journalctl() XidBackend {
	return &journalctlBackend{run: runJournalctl}
}

// journalctlBackend reads Xid lines from the journal. run is a seam so
// tests can exercise the parsing and error handling without a host
// journal; production uses runJournalctl.
type journalctlBackend struct {
	run runFunc
}

// Measure reads the current boot's Xid lines. It scopes the read to
// kernel-transport entries (--dmesg) and greps for "Xid": Xid is an NVRM
// kernel message, so this is exact coverage and keeps a large journal from
// tripping the collection timeout. See [readJournal] for the clean-boot vs
// unreadable-journal handling.
func (b *journalctlBackend) Measure(ctx context.Context) ([]health.Measurement, error) {
	driverVersion, _ := xidparse.DetectDriverVersion()
	return readJournal(ctx, b.run, journalMatcher{
		args:    []string{"--output=cat", "--grep=Xid"},
		subject: "Xid state",
		parse:   func(lines []string) health.Measurement { return xidparse.ParseXid(sinceBoot, lines, driverVersion) },
		errored: func(err error) health.Measurement {
			return xid.ErroredXid(sinceBoot, err, xid.MeasurementSignals...)
		},
	})
}

// MockXid returns a backend that parses the provided log lines with the
// real parser, as if journalctl had returned them. It is the entry
// point for injecting a fake syslog in tests: pass raw "NVRM: Xid …"
// lines and the catalog sees realistically-parsed measurements. Pass no
// lines to simulate a clean boot.
//
//	sensor := toolkit.NewXidSensor(toolkit.MockXid(
//	    "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
//	))
func MockXid(lines ...string) XidBackend {
	return &mockXidBackend{lines: lines}
}

type mockXidBackend struct {
	lines []string
}

func (m *mockXidBackend) Measure(_ context.Context) ([]health.Measurement, error) {
	return []health.Measurement{xidparse.ParseXid(sinceBoot, m.lines)}, nil
}
