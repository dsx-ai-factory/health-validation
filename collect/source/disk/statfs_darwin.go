// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func fragmentSize(stat *unix.Statfs_t) uint64 { return uint64(stat.Bsize) }

func openCapacityPath(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
}

func verifyCapacityMount(*os.File, uint64) error {
	return fmt.Errorf("Linux mountinfo descriptor identity is unavailable on this platform: %w", ErrUnsupported)
}
