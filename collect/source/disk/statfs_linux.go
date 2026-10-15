// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"errors"
	"fmt"
	"io/fs"
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

// STATX_MNT_ID is the mountinfo ID of the opened descriptor, not a second
// pathname lookup. The descriptor pins that mount through Fstatfs.
func verifyCapacityMount(file *os.File, expected uint64) error {
	var stat unix.Statx_t
	err := unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_STATX_DONT_SYNC, unix.STATX_MNT_ID, &stat)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) {
			return fmt.Errorf("descriptor mount identity: %w: %v", ErrUnsupported, err)
		}
		return fmt.Errorf("descriptor mount identity: %w", err)
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 {
		return fmt.Errorf("descriptor mount identity unavailable: %w", ErrUnsupported)
	}
	if stat.Mnt_id != expected {
		return fmt.Errorf("mount %d replaced or capacity view differs (opened mount %d): %w", expected, stat.Mnt_id, fs.ErrNotExist)
	}
	return nil
}
