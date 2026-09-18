//go:build linux

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg

import (
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func openKmsg() (*os.File, time.Time, string, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, time.Time{}, "", err
	}
	bootTime, err := parseBootTime(string(stat))
	if err != nil {
		return nil, time.Time{}, "", err
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, time.Time{}, "", err
	}
	// NewFile registers the nonblocking descriptor with Go's poller so Close
	// interrupts a blocked Read even when the kernel emits no further messages.
	fd, err := unix.Open("/dev/kmsg", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return os.NewFile(uintptr(fd), "/dev/kmsg"), bootTime, strings.TrimSpace(string(bootID)), nil
}
