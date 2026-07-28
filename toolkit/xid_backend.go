/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package toolkit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/xid"
)

// sinceBoot labels measurements collected with journalctl --boot.
const sinceBoot = "boot"

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
// current boot's journal by running:
//
//	journalctl --boot --no-pager --output=cat --grep=NVRM: Xid
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
// journal; production uses runJournalctl. It takes the journalctl
// arguments so the backend can issue both the Xid grep and a liveness
// probe through the same seam.
type journalctlBackend struct {
	run func(ctx context.Context, args ...string) ([]byte, error)
}

func (b *journalctlBackend) Measure(ctx context.Context) ([]health.Measurement, error) {
	// The caller's context bounds the read; Collect's watchdog supplies the
	// deadline, so the operator's collection timeout governs the journal read.
	out, err := b.run(ctx, "--boot", "--no-pager", "--output=cat", "--grep=Xid")
	if err != nil {
		// Exit status 1 with no output is journalctl's "no matching
		// entries" signal. That is ambiguous: it means either a genuinely
		// Xid-free boot, OR that journalctl is reading an empty/absent
		// journal (missing mount, container's own machine-id, unreadable
		// format) — the same signal in both cases. Before trusting a clean
		// result, confirm the journal is actually readable; if it is not,
		// surface UNKNOWN via ErroredXid rather than a false PASS.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
			if probeErr := b.journalReadable(ctx); probeErr != nil {
				return []health.Measurement{
					xid.ErroredXid(sinceBoot, fmt.Errorf("journal unreadable, cannot confirm Xid state: %w", probeErr), xid.MeasurementSignals...),
				}, nil
			}
			return []health.Measurement{ParseXid(sinceBoot, nil)}, nil
		}
		return []health.Measurement{
			xid.ErroredXid(sinceBoot, fmt.Errorf("journalctl: %w", err), xid.MeasurementSignals...),
		}, nil
	}

	return []health.Measurement{ParseXid(sinceBoot, splitLines(out))}, nil
}

// journalReadable confirms the current boot's journal holds at least one
// entry. A live host journal always does — a booted Linux host logs
// continuously — so an empty result (or a read error) means journalctl is
// pointed at an empty or absent journal, not that the boot was Xid-free.
// It returns nil only when the journal is demonstrably readable.
func (b *journalctlBackend) journalReadable(ctx context.Context) error {
	out, err := b.run(ctx, "--boot", "--no-pager", "--output=cat", "-n", "1")
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return errors.New("current boot has no journal entries")
	}
	return nil
}

// runJournalctl executes journalctl with the given arguments and returns
// its stdout.
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
	return []health.Measurement{ParseXid(sinceBoot, m.lines)}, nil
}
