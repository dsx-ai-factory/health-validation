package toolkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// sinceBoot labels measurements collected with journalctl --boot.
const sinceBoot = "boot"

// runFunc runs journalctl with the given arguments and returns its stdout. It
// is the seam the journal-backed backends inject so tests can exercise parsing
// and error handling without a host journal; production uses [runJournalctl].
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

// journalMatcher is the per-domain configuration a journal-backed backend
// supplies to [readJournal]: how to scope and filter the read, and how to turn
// the result — matching lines, or an unreadable journal — into a measurement.
type journalMatcher struct {
	// parse builds the measurement from the matching lines (nil on a clean
	// boot); errored builds the measurement recorded when the journal could
	// not be read, so the dependent check fails rather than falsely passing.
	parse   func(lines []string) health.Measurement
	errored func(err error) health.Measurement
	// match selects the lines passed to parse; journalctl itself is not filtered.
	match func(line string) bool
	// subject names the state being read, for the "cannot confirm <subject>"
	// message when the journal is unreadable (e.g. "Xid state", "link state").
	subject string
	// args are the journalctl flags for the primary read, such as the output
	// format and time window. readJournal always prepends --dmesg, --boot,
	// --no-pager, and --quiet, then filters the returned kernel lines through
	// match.
	args []string
}

// readJournal reads the current boot's kernel journal, retains lines accepted
// by the matcher, and turns the result into a single measurement. --dmesg limits
// the read to kernel entries so a large full journal does not consume the
// collection timeout.
//
// An empty kernel journal (exit 1, empty output) is ambiguous between a boot
// with no kernel entries and an absent/unreadable journal. Before trusting it,
// [journalReadable] confirms the journal itself is readable. --quiet keeps the
// informational "-- No entries --" banner out of stdout so this distinction is
// reliable across journalctl versions.
func readJournal(ctx context.Context, run runFunc, m journalMatcher) ([]health.Measurement, error) {
	// The caller's context bounds the read; Collect's watchdog supplies the
	// deadline, so the operator's collection timeout governs the journal read.
	args := append([]string{"--dmesg", "--boot", "--no-pager", "--quiet"}, m.args...)
	out, err := run(ctx, args...)
	if err != nil {
		var exitErr *exec.ExitError
		noEntries := errors.As(err, &exitErr) &&
			exitErr.ExitCode() == 1 &&
			len(bytes.TrimSpace(out)) == 0 &&
			len(bytes.TrimSpace(exitErr.Stderr)) == 0
		if !noEntries {
			return []health.Measurement{m.errored(journalCommandError(err))}, nil
		}
	}

	lines := make([]string, 0)
	for _, line := range splitLines(out) {
		if m.match(line) {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		if probeErr := journalReadable(ctx, run); probeErr != nil {
			return []health.Measurement{m.errored(fmt.Errorf("journal unreadable, cannot confirm %s: %w", m.subject, probeErr))}, nil
		}
	}
	return []health.Measurement{m.parse(lines)}, nil
}

// journalReadable confirms the current boot's journal holds at least one entry.
// A live host journal always does — a booted Linux host logs continuously — so
// an empty result (or a read error) means journalctl is pointed at an empty or
// absent journal, not that the boot was event-free. It carries no domain filter,
// so it counts entries rather than matching the pattern being validated. Returns
// nil only when the journal is demonstrably readable.
func journalReadable(ctx context.Context, run runFunc) error {
	out, err := run(ctx, "--boot", "--no-pager", "--output=cat", "-n", "1")
	if err != nil {
		return journalCommandError(err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return errors.New("current boot has no journal entries")
	}
	return nil
}

// runJournalctl executes journalctl with the given arguments and returns its
// stdout.
func runJournalctl(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "journalctl", args...).Output()
}

// splitLines splits command output into non-empty lines.
func splitLines(out []byte) []string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// journalCommandError adds journalctl's captured stderr to a command error.
func journalCommandError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return fmt.Errorf("journalctl: %w: %s", err, stderr)
		}
	}
	return fmt.Errorf("journalctl: %w", err)
}
