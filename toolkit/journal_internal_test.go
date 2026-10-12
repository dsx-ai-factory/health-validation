package toolkit

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// linkFlapMatcher is the matcher the RDMA link-flap backend supplies. It needs
// --output=short-full for per-line timestamps and filters link lines in Go.
func linkFlapMatcher(since string) journalMatcher {
	return journalMatcher{
		args:    []string{"--since=" + since, "--output=short-full"},
		subject: "link state",
		match:   linkFlapPattern.MatchString,
		parse:   func(lines []string) health.Measurement { return ParseLinkFlaps(since, lines) },
		errored: func(err error) health.Measurement {
			return infiniband.ErroredLinkFlap(since, err, infiniband.MeasurementSignals...)
		},
	}
}

// TestReadJournalCleanBootIsNotErrored pins the handling of an empty kernel
// journal. Some journalctl versions print "-- No entries --" on stdout unless
// --quiet is present; that banner must not turn an otherwise readable journal
// into a collection failure.
func TestReadJournalCleanBootIsNotErrored(t *testing.T) {
	const since = "2026-08-31 13:00:00"

	// The liveness probe (-n 1) returns a line so the journal itself reads as
	// readable; the scoped kernel read has no entries.
	run := func(_ context.Context, args ...string) ([]byte, error) {
		if slices.Contains(args, "-n") {
			return []byte("some unrelated log line\n"), nil
		}
		if slices.Contains(args, "--quiet") {
			return nil, exitOne(t)
		}
		return []byte("-- No entries --\n"), exitOne(t)
	}

	ms, err := readJournal(context.Background(), run, linkFlapMatcher(since))
	if err != nil {
		t.Fatalf("readJournal: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d measurements, want 1", len(ms))
	}
	if errs := ms[0].Errors(); len(errs) != 0 {
		t.Errorf("clean boot recorded %d collection error(s), want 0: %v", len(errs), errs)
	}
}

// TestReadJournalQuietDoesNotHideMatches guards the other half: --quiet must
// suppress only the informational banner, never matching lines.
func TestReadJournalQuietDoesNotHideMatches(t *testing.T) {
	const since = "2026-08-31 13:00:00"
	// A flap is a "Link down" paired with the next "Link up" on the same
	// interface, so both lines are needed to produce one event.
	lines := []string{
		"Mon 2026-08-31 13:05:00 UTC host kernel: mlx5_core 0016:01:00.0 enP22s22f0np0: Link down",
		"Mon 2026-08-31 13:07:00 UTC host kernel: mlx5_core 0016:01:00.0 enP22s22f0np0: Link up",
	}

	run := func(_ context.Context, args ...string) ([]byte, error) {
		if !slices.Contains(args, "--quiet") {
			t.Error("readJournal must pass --quiet to suppress the no-entries banner")
		}
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	}

	ms, err := readJournal(context.Background(), run, linkFlapMatcher(since))
	if err != nil {
		t.Fatalf("readJournal: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d measurements, want 1", len(ms))
	}
	if errs := ms[0].Errors(); len(errs) != 0 {
		t.Errorf("parsed measurement recorded %d collection error(s), want 0: %v", len(errs), errs)
	}

	lf, ok := ms[0].(infiniband.LinkFlapMeasurement)
	if !ok {
		t.Fatalf("measurement type = %T, want infiniband.LinkFlapMeasurement", ms[0])
	}
	if len(lf.FlapEvents) == 0 {
		t.Error("matching link line was not parsed into a flap event")
	}
}
