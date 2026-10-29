//go:build linux

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package rdmatopo

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func checkExecCapabilities() error {
	status, err := os.ReadFile("/proc/thread-self/status")
	if err != nil {
		return fmt.Errorf("%w: read process capabilities: %w", ErrUnreadable, err)
	}
	rootPrivileges := false
	if os.Geteuid() == 0 {
		bits, err := unix.PrctlRetInt(unix.PR_GET_SECUREBITS, 0, 0, 0, 0)
		if err != nil {
			return fmt.Errorf("%w: read securebits: %w", ErrUnreadable, err)
		}
		const secbitNoRoot = 1
		rootPrivileges = bits&secbitNoRoot == 0
	}
	return checkCapabilityInheritance(string(status), rootPrivileges)
}
