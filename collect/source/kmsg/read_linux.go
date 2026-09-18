//go:build linux

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// SPDX-FileCopyrightText: Copyright 2016 Euan Kemp
// Adapted from github.com/leptonai/gpud/pkg/kmsg/watcher.go (Apache-2.0),
// derived from github.com/euank/go-kmsg-parser.

package kmsg

import (
	"context"

	"golang.org/x/sys/unix"
)

// ReadAll reads the currently available kernel ring buffer without following.
// It retains repeated message bodies with distinct sequence numbers. Lost
// records (EPIPE) are reported after draining the remaining records.
func ReadAll(ctx context.Context) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, bootTime, bootID, err := openKmsg()
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}
	if ctrlErr := raw.Control(func(fd uintptr) { err = unix.SetNonblock(int(fd), true) }); ctrlErr != nil {
		return nil, ctrlErr
	}
	if err != nil {
		return nil, err
	}
	return readRecords(ctx, bootTime, bootID, func(buf []byte) (int, error) {
		var n int
		var readErr error
		connErr := raw.Read(func(fd uintptr) bool {
			n, readErr = unix.Read(int(fd), buf)
			return true // EAGAIN ends the snapshot; do not wait for another record.
		})
		if readErr == nil {
			readErr = connErr
		}
		return n, readErr
	})
}
