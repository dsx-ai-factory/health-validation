package toolkit

import (
	"bufio"
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

const (
	// maxJournalLineBytes bounds a single journal line; kernel messages are far
	// shorter, so a longer line fails the read rather than passing silently.
	maxJournalLineBytes = 1 << 20
	// maxJournalStderrBytes bounds the diagnostic text retained on command failure.
	maxJournalStderrBytes = 64 << 10
)

// journalRunResult holds the filtered output and whether stdout contained any
// non-blank output, including lines rejected by the matcher.
type journalRunResult struct {
	lines      []string
	outputSeen bool
}

// boundedStderr retains a bounded prefix while reporting every byte consumed.
type boundedStderr struct {
	buf []byte
}

func (b *boundedStderr) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxJournalStderrBytes - len(b.buf)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		b.buf = append(b.buf, p[:remaining]...)
	}
	return n, nil
}

// runFunc runs journalctl with the given arguments and returns the stdout lines
// accepted by match. It is the seam the journal-backed backends inject so tests
// can exercise parsing and error handling without a host journal; production
// uses [runJournalctl].
type runFunc func(ctx context.Context, match func(string) bool, args ...string) (journalRunResult, error)

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
// No matching lines is ambiguous between an event-free boot and an
// absent/unreadable journal, so [journalReadable] must confirm the journal is
// readable before the result is trusted. Exit 1 with empty stderr is
// journalctl's "no entries"; exit 1 with stderr is a failed read (e.g. an
// unsupported option) and is never treated as clean. --quiet keeps the
// informational "-- No entries --" banner out of the output.
func readJournal(ctx context.Context, run runFunc, m journalMatcher) ([]health.Measurement, error) {
	// The caller's context bounds the read; Collect's watchdog supplies the
	// deadline, so the operator's collection timeout governs the journal read.
	args := append([]string{"--dmesg", "--boot", "--no-pager", "--quiet"}, m.args...)
	result, err := run(ctx, m.match, args...)
	if err != nil {
		var exitErr *exec.ExitError
		noEntries := errors.As(err, &exitErr) &&
			exitErr.ExitCode() == 1 &&
			!result.outputSeen &&
			len(bytes.TrimSpace(exitErr.Stderr)) == 0
		if !noEntries {
			return []health.Measurement{m.errored(journalCommandError(err))}, nil
		}
	}
	if len(result.lines) == 0 {
		if probeErr := journalReadable(ctx, run); probeErr != nil {
			return []health.Measurement{m.errored(fmt.Errorf("journal unreadable, cannot confirm %s: %w", m.subject, probeErr))}, nil
		}
	}
	return []health.Measurement{m.parse(result.lines)}, nil
}

// journalReadable confirms the current boot's journal holds at least one entry.
// A live host journal always does — a booted Linux host logs continuously — so
// an empty result (or a read error) means journalctl is pointed at an empty or
// absent journal, not that the boot was event-free. It carries no domain filter,
// so it counts entries rather than matching the pattern being validated. Returns
// nil only when the journal is demonstrably readable.
func journalReadable(ctx context.Context, run runFunc) error {
	nonBlank := func(line string) bool { return strings.TrimSpace(line) != "" }
	result, err := run(ctx, nonBlank, "--boot", "--no-pager", "--output=cat", "-n", "1")
	if err != nil {
		return journalCommandError(err)
	}
	if len(result.lines) == 0 {
		return errors.New("current boot has no journal entries")
	}
	return nil
}

// runJournalctl streams journalctl's stdout, retaining only matching lines and
// a bounded stderr sample.
func runJournalctl(ctx context.Context, match func(string) bool, args ...string) (journalRunResult, error) {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	var stderr boundedStderr
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return journalRunResult{}, err
	}
	if startErr := cmd.Start(); startErr != nil {
		return journalRunResult{}, startErr
	}

	var result journalRunResult
	sc := bufio.NewScanner(stdout)
	sc.Buffer(nil, maxJournalLineBytes)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) != "" {
			result.outputSeen = true
		}
		if match(line) {
			result.lines = append(result.lines, line)
		}
	}
	if scanErr := sc.Err(); scanErr != nil {
		// Close stdout, terminate journalctl, and release its process resources.
		_ = stdout.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return journalRunResult{}, fmt.Errorf("reading journalctl output: %w", scanErr)
	}

	err = cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitErr.Stderr = stderr.buf
	}
	return result, err
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
