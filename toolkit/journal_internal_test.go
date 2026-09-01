package toolkit

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// linkFlapMatcher is the matcher the RDMA link-flap backend supplies, which is
// the read that exposed the banner: it needs --output=short-full for its
// per-line timestamps, and that format does not suppress "-- No entries --".
func linkFlapMatcher(since string) journalMatcher {
	return journalMatcher{
		args:    []string{"--since=" + since, "--output=short-full", "--grep=" + linkFlapGrep},
		subject: "link state",
		parse:   func(lines []string) health.Measurement { return ParseLinkFlaps(since, lines) },
		errored: func(err error) health.Measurement {
			return infiniband.ErroredLinkFlap(since, err, infiniband.MeasurementSignals...)
		},
	}
}

// TestReadJournalCleanBootIsNotErrored pins the behaviour against real
// journalctl rather than an idealised one: journalctl 255 answers a --grep that
// matches nothing with exit 1 *and* "-- No entries --" on stdout, so a clean
// host only reads as clean while readJournal passes --quiet. Without it every
// event-free node reports the journal as unreadable, which for the link-flap
// check means CRITICAL and a drain on healthy hardware.
func TestReadJournalCleanBootIsNotErrored(t *testing.T) {
	const since = "2026-08-31 13:00:00"

	// run mimics journalctl 255. The liveness probe (-n 1) returns a line so
	// the journal itself reads as readable; the grep matches nothing.
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
