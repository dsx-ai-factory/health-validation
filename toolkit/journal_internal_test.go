// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// cannedRun adapts a fake returning raw stdout to runFunc, applying match the
// way [runJournalctl] does.
func cannedRun(f func(ctx context.Context, args ...string) ([]byte, error)) runFunc {
	return func(ctx context.Context, match func(string) bool, args ...string) (journalRunResult, error) {
		out, err := f(ctx, args...)
		result := journalRunResult{outputSeen: len(strings.TrimSpace(string(out))) > 0}
		for _, line := range strings.Split(string(out), "\n") {
			if line != "" && match(line) {
				result.lines = append(result.lines, line)
			}
		}
		return result, err
	}
}

// linkFlapMatcher is the matcher the RDMA link-flap backend supplies. It needs
// --output=short-full for per-line timestamps and filters link lines in Go.
func linkFlapMatcher(since string) journalMatcher {
	return journalMatcher{
		args:    []string{"--since=" + since, "--output=short-full"},
		subject: "link state",
		match:   linkFlapPattern.MatchString,
		parse:   func(lines []string) health.Measurement { return parseLinkFlaps(since, lines) },
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
	run := cannedRun(func(_ context.Context, args ...string) ([]byte, error) {
		if slices.Contains(args, "-n") {
			return []byte("some unrelated log line\n"), nil
		}
		if slices.Contains(args, "--quiet") {
			return nil, exitOne(t)
		}
		return []byte("-- No entries --\n"), exitOne(t)
	})

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

func TestReadJournalExitOneWithStdoutIsErrored(t *testing.T) {
	const since = "2026-08-31 13:00:00"
	run := cannedRun(func(_ context.Context, args ...string) ([]byte, error) {
		if slices.Contains(args, "-n") {
			return []byte("some journal entry\n"), nil
		}
		return []byte("partial non-matching output\n"), exitOne(t)
	})

	ms, err := readJournal(context.Background(), run, linkFlapMatcher(since))
	if err != nil {
		t.Fatalf("readJournal: %v", err)
	}
	if errs := ms[0].Errors(); len(errs) == 0 {
		t.Fatal("exit 1 with stdout must be recorded as a collection error")
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

	run := cannedRun(func(_ context.Context, args ...string) ([]byte, error) {
		if !slices.Contains(args, "--quiet") {
			t.Error("readJournal must pass --quiet to suppress the no-entries banner")
		}
		return []byte(strings.Join(lines, "\n") + "\n"), nil
	})

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

// TestRunJournalctl exercises the real process plumbing against a fake
// journalctl on PATH: line filtering, stderr capture on exit 1 (the COS
// "PCRE2 support is not compiled in." failure), and an over-long line.
func TestRunJournalctl(t *testing.T) {
	isXid := func(line string) bool { return strings.Contains(line, "NVRM: Xid ") }
	tests := []struct {
		name            string
		script          string
		wantStderr      string
		wantLines       []string
		wantStderrBytes int
		wantOutputSeen  bool
		wantErr         bool
	}{
		{
			name:           "keeps only matching lines",
			script:         "printf 'unrelated\\nNVRM: Xid (PCI:0000:01:00): 79\\nother\\n'",
			wantLines:      []string{"NVRM: Xid (PCI:0000:01:00): 79"},
			wantOutputSeen: true,
		},
		{
			name:       "exit one with stderr is reported",
			script:     "echo 'PCRE2 support is not compiled in.' >&2; exit 1",
			wantErr:    true,
			wantStderr: "PCRE2 support is not compiled in.",
		},
		{
			name:    "over-long line fails the read",
			script:  "head -c 2000000 /dev/zero | tr '\\0' x; echo",
			wantErr: true,
		},
		{
			name:            "bounds captured stderr",
			script:          "head -c 100000 /dev/zero | tr '\\0' e >&2; exit 1",
			wantStderrBytes: maxJournalStderrBytes,
			wantErr:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\n" + tt.script + "\n"
			if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			result, err := runJournalctl(context.Background(), isXid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(result.lines, tt.wantLines) {
				t.Errorf("lines = %q, want %q", result.lines, tt.wantLines)
			}
			if result.outputSeen != tt.wantOutputSeen {
				t.Errorf("outputSeen = %v, want %v", result.outputSeen, tt.wantOutputSeen)
			}
			if tt.wantStderr != "" {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || strings.TrimSpace(string(exitErr.Stderr)) != tt.wantStderr {
					t.Errorf("err = %v, want *exec.ExitError with stderr %q", err, tt.wantStderr)
				}
			}
			if tt.wantStderrBytes > 0 {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("err = %v, want *exec.ExitError", err)
				}
				if got := len(exitErr.Stderr); got != tt.wantStderrBytes {
					t.Errorf("stderr bytes = %d, want %d", got, tt.wantStderrBytes)
				}
			}
		})
	}
}
