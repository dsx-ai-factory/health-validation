// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package journal reads the current boot's kernel entries from the systemd
// journal through journalctl.
package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrNoEntries reports a read that returned no kernel entries for the
	// current boot.
	ErrNoEntries = errors.New("no kernel entries for the current boot")
	// ErrMalformedEntry reports journalctl output that is not a valid JSON
	// entry.
	ErrMalformedEntry = errors.New("malformed journal entry")
)

const (
	maxLine     = 1 << 20
	maxStderr   = 4 << 10
	maxErrorMsg = 256
)

// kernelBootArgs reads every kernel entry of the current boot. There is no
// --grep: it needs PCRE2 and systemd 237+, and a journalctl without it exits
// 1 with empty output, which read as a clean boot. --quiet keeps the
// "-- No entries --" banner off stdout. --output-fields needs systemd 236+;
// JSON output always adds __CURSOR, __REALTIME_TIMESTAMP and _BOOT_ID. --all
// stops journalctl nulling fields longer than 4096 bytes.
var kernelBootArgs = []string{"--dmesg", "--boot", "--no-pager", "--quiet", "--output=json", "--output-fields=MESSAGE", "--all"}

// Runner runs journalctl with args, writing its stdout and stderr.
type Runner func(ctx context.Context, args []string, stdout, stderr io.Writer) error

// Entry is one journal entry. Realtime is zero when the entry has no
// positive __REALTIME_TIMESTAMP.
type Entry struct {
	Realtime time.Time
	BootID   string
	Cursor   string
	Message  string
}

// Reader reads kernel entries from the journal.
type Reader struct {
	run Runner
}

// New returns a Reader that runs journalctl through run, or from PATH when
// run is nil.
func New(run Runner) *Reader {
	if run == nil {
		run = runJournalctl
	}
	return &Reader{run: run}
}

// ReadKernelBoot calls each for every kernel entry of the current boot, in
// journal order. An error from each stops the read and is returned.
func (r *Reader) ReadKernelBoot(ctx context.Context, each func(Entry) error) error {
	entries, err := r.readKernelBoot(ctx, kernelBootArgs, each, false)
	if err == nil && entries == 0 {
		return ErrNoEntries
	}
	return err
}

// ReadKernelBootRange limits the current boot's kernel entries to inclusive
// Unix-second bounds. Nil bounds are unbounded. An empty bounded read succeeds
// only when an unbounded kernel-journal probe confirms the journal is readable.
func (r *Reader) ReadKernelBootRange(ctx context.Context, from, to *int64, each func(Entry) error) error {
	if from == nil && to == nil {
		return r.ReadKernelBoot(ctx, each)
	}
	if from != nil && *from < 0 {
		return fmt.Errorf("start time must be non-negative")
	}
	if to != nil && *to <= 0 {
		return fmt.Errorf("end time must be positive")
	}
	if from != nil && to != nil && *from >= *to {
		return fmt.Errorf("start time must be less than end time")
	}
	args := append([]string(nil), kernelBootArgs...)
	if from != nil {
		args = append(args, fmt.Sprintf("--since=@%d", *from))
	}
	if to != nil {
		args = append(args, fmt.Sprintf("--until=@%d", *to))
	}
	entries, err := r.readKernelBoot(ctx, args, each, true)
	if err != nil || entries > 0 {
		return err
	}
	probeArgs := append(append([]string(nil), kernelBootArgs...), "-n", "1")
	entries, err = r.readKernelBoot(ctx, probeArgs, func(Entry) error { return nil }, false)
	if err == nil && entries == 0 {
		return ErrNoEntries
	}
	return err
}

func (r *Reader) readKernelBoot(ctx context.Context, args []string, each func(Entry) error, allowNoEntriesExit bool) (int, error) {
	lines := &lineWriter{each: each}
	stderr := &prefixWriter{max: maxStderr}
	runErr := r.run(ctx, args, lines, stderr)
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("journalctl: %w", err)
	}
	if lines.err != nil {
		return 0, lines.err
	}
	// Some journalctl versions return exit 1 for an empty time window.
	var exitErr *exec.ExitError
	if allowNoEntriesExit && errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 &&
		lines.entries == 0 && len(bytes.TrimSpace(lines.pending)) == 0 &&
		strings.TrimSpace(stderr.buf.String()) == "" {
		runErr = nil
	}
	if runErr != nil {
		if detail := boundedUTF8(strings.TrimSpace(stderr.buf.String()), maxErrorMsg); detail != "" {
			return 0, fmt.Errorf("journalctl: %w: %s", runErr, detail)
		}
		return 0, fmt.Errorf("journalctl: %w", runErr)
	}
	if err := lines.flush(); err != nil {
		return 0, err
	}
	return lines.entries, nil
}

func runJournalctl(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// A killed chroot shim can leave a child holding the output pipe.
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

// lineWriter decodes newline-delimited JSON entries. After the first error it
// discards the rest so journalctl never blocks on a full pipe. A line that
// does not decode fails the read: journalctl 236-239 leaves a corrupt entry
// unterminated, so the next entry shares its line and skipping it could drop
// an Xid.
type lineWriter struct {
	err     error
	each    func(Entry) error
	pending []byte
	entries int
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for w.err == nil && len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.pending = append(w.pending, p...)
			if len(w.pending) > maxLine {
				w.err = fmt.Errorf("%w: line exceeds %d bytes", ErrMalformedEntry, maxLine)
			}
			break
		}
		w.pending = append(w.pending, p[:i]...)
		p = p[i+1:]
		w.line()
	}
	return n, nil
}

func (w *lineWriter) flush() error {
	if w.err == nil && len(bytes.TrimSpace(w.pending)) > 0 {
		w.line()
	}
	return w.err
}

func (w *lineWriter) line() {
	line := w.pending
	w.pending = w.pending[:0]
	if len(line) > maxLine {
		w.err = fmt.Errorf("%w: line exceeds %d bytes", ErrMalformedEntry, maxLine)
		return
	}
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	entry, err := decode(line)
	if err != nil {
		w.err = err
		return
	}
	w.entries++
	w.err = w.each(entry)
}

func decode(line []byte) (Entry, error) {
	var raw struct {
		Cursor   string          `json:"__CURSOR"`
		Realtime string          `json:"__REALTIME_TIMESTAMP"`
		BootID   string          `json:"_BOOT_ID"`
		Message  json.RawMessage `json:"MESSAGE"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, fmt.Errorf("%w: %v", ErrMalformedEntry, err)
	}
	entry := Entry{BootID: raw.BootID, Cursor: raw.Cursor}
	if raw.Realtime != "" {
		usec, err := strconv.ParseInt(raw.Realtime, 10, 64)
		if err != nil {
			return Entry{}, fmt.Errorf("%w: __REALTIME_TIMESTAMP is not a number", ErrMalformedEntry)
		}
		if usec > 0 {
			entry.Realtime = time.UnixMicro(usec).UTC()
		}
	}
	message, err := decodeMessage(raw.Message)
	if err != nil {
		return Entry{}, err
	}
	entry.Message = strings.ToValidUTF8(message, "�")
	return entry, nil
}

// decodeMessage accepts journalctl's string form and the byte-array form it
// uses for values that are not valid UTF-8.
func decodeMessage(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var octets []byte
	var values []int
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", fmt.Errorf("%w: MESSAGE is neither a string nor a byte array", ErrMalformedEntry)
	}
	for _, v := range values {
		if v < 0 || v > 255 {
			return "", fmt.Errorf("%w: MESSAGE byte out of range", ErrMalformedEntry)
		}
		octets = append(octets, byte(v))
	}
	return string(octets), nil
}

// prefixWriter keeps the first max bytes written to it.
type prefixWriter struct {
	buf bytes.Buffer
	max int
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// boundedUTF8 returns valid UTF-8 of at most n bytes, cut on a rune boundary.
func boundedUTF8(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
