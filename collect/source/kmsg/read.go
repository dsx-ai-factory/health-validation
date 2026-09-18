// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// SPDX-FileCopyrightText: Copyright 2016 Euan Kemp
// Adapted from github.com/leptonai/gpud/pkg/kmsg/watcher.go (Apache-2.0),
// derived from github.com/euank/go-kmsg-parser.

package kmsg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const readBufferSize = 8192

// GPUd's snapshot loop, with injectable reads, cancellation, boot identity,
// and partial-result/error reporting. Content deduplication stays outside.
func readRecords(ctx context.Context, bootTime time.Time, bootID string, read func([]byte) (int, error)) ([]Message, error) {
	var lost error
	msgs := make([]Message, 0)
	buf := make([]byte, readBufferSize)
	for {
		// Each read call gives us one full message.
		// https://www.kernel.org/doc/Documentation/ABI/testing/dev-kmsg
		if err := ctx.Err(); err != nil {
			return msgs, errors.Join(lost, err)
		}
		n, err := read(buf)

		switch {
		case err == nil:
			// no-op

		case errors.Is(err, syscall.EINTR):
			continue

		case errors.Is(err, syscall.EPIPE):
			lost = syscall.EPIPE
			// error that indicates "continuation" of the ring buffer
			continue

		case errors.Is(err, syscall.EAGAIN):
			// end of ring buffer in non-follow mode
			return msgs, lost

		default:
			return msgs, errors.Join(lost, fmt.Errorf("unexpected kmsg reading error: %w", err))
		}

		line := string(buf[:n])
		if len(line) == 0 {
			continue
		}

		msg, err := parseLine(bootTime, line)
		if err != nil {
			return msgs, errors.Join(lost, fmt.Errorf("%w: malformed kmsg message: %w (%q)", ErrMalformedRecord, err, line))
		}

		msg.BootID = bootID

		msgs = append(msgs, *msg)
	}
}

// GPUd's native Linux boot-time path uses /proc/stat btime. Read that origin
// directly to avoid pulling a host-inspection dependency into the source.
func parseBootTime(stat string) (time.Time, error) {
	for _, line := range strings.Split(stat, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "btime" {
			continue
		}
		if len(fields) != 2 {
			return time.Time{}, fmt.Errorf("invalid proc stat btime: %q", line)
		}
		seconds, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || seconds < 0 {
			return time.Time{}, fmt.Errorf("invalid proc stat btime: %q", line)
		}
		return time.Unix(seconds, 0), nil
	}
	return time.Time{}, fmt.Errorf("proc stat has no btime")
}
