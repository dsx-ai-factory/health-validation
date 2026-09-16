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

// runFunc runs an external command with the given arguments and returns its
// stdout. It is the seam the command-backed backends inject so tests can
// exercise parsing and error handling without the host tool; production uses
// [runJournalctl] for the journal reads and [runIBStat] for ibstat.
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

// journalMatcher is the per-domain configuration a journal-backed backend
// supplies to [readJournal]: how to scope and grep the read, and how to turn
// the result — matching lines, or an unreadable journal — into a measurement.
type journalMatcher struct {
	// parse builds the measurement from the matching lines (nil on a clean
	// boot); errored builds the measurement recorded when the journal could
	// not be read, so the dependent check fails rather than falsely passing.
	parse   func(lines []string) health.Measurement
	errored func(err error) health.Measurement
	// subject names the state being read, for the "cannot confirm <subject>"
	// message when the journal is unreadable (e.g. "Xid state", "link state").
	subject string
	// args are the journalctl flags for the primary read — the output format
	// and the --grep. readJournal always prepends --dmesg --boot --no-pager.
	args []string
}

// readJournal runs journalctl for the current boot and turns the result into a
// single measurement. --dmesg scopes the read to kernel-transport entries,
// turning the --grep from a full-boot full-text scan into an indexed scan over
// the kernel subset — what keeps a large journal from tripping the collection
// timeout, most acutely on a clean boot where --grep must read the whole boot
// to prove zero matches.
//
// journalctl's "no matching entries" (exit 1, empty output) is ambiguous
// between a genuinely event-free boot and an empty/unreadable journal (missing
// mount, container's own machine-id, unreadable format). Before trusting a
// clean result, [journalReadable] confirms the journal is actually readable; if
// it is not, the result is an errored measurement rather than a false PASS.
//
// --quiet is required for that exit-1-and-empty test to hold. Without it
// journalctl writes "-- No entries --" to *stdout* (not stderr) when a --grep
// matches nothing, so a clean boot arrives as exit 1 with 17 bytes of output
// and falls through to the errored path -- turning every healthy node CRITICAL.
// --output=cat happens to suppress the banner, which is why the Xid read was
// unaffected while the link-flap read (--output=short-full, needed for its
// per-line timestamps) reported an unreadable journal on every clean host.
func readJournal(ctx context.Context, run runFunc, m journalMatcher) ([]health.Measurement, error) {
	// The caller's context bounds the read; Collect's watchdog supplies the
	// deadline, so the operator's collection timeout governs the journal read.
	args := append([]string{"--dmesg", "--boot", "--no-pager", "--quiet"}, m.args...)
	out, err := run(ctx, args...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
			if probeErr := journalReadable(ctx, run); probeErr != nil {
				return []health.Measurement{m.errored(fmt.Errorf("journal unreadable, cannot confirm %s: %w", m.subject, probeErr))}, nil
			}
			return []health.Measurement{m.parse(nil)}, nil
		}
		return []health.Measurement{m.errored(fmt.Errorf("journalctl: %w", err))}, nil
	}
	return []health.Measurement{m.parse(splitLines(out))}, nil
}

// journalReadable confirms the current boot's journal holds at least one entry.
// A live host journal always does — a booted Linux host logs continuously — so
// an empty result (or a read error) means journalctl is pointed at an empty or
// absent journal, not that the boot was event-free. It carries no --grep, so it
// counts entries rather than matching the domain pattern it validates. Returns
// nil only when the journal is demonstrably readable.
func journalReadable(ctx context.Context, run runFunc) error {
	out, err := run(ctx, "--boot", "--no-pager", "--output=cat", "-n", "1")
	if err != nil {
		return err
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
