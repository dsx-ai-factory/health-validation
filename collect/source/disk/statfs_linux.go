// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"os"

	"golang.org/x/sys/unix"
)

func fragmentSize(stat *unix.Statfs_t) uint64 {
	if stat.Frsize > 0 {
		return uint64(stat.Frsize)
	}
	if stat.Bsize > 0 {
		return uint64(stat.Bsize)
	}
	return 0
}

func openCapacityPath(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, unix.O_PATH|unix.O_DIRECTORY, 0)
}
