// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin

package disk

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func nativeUsage(hostRoot string, mount Mount) (Usage, error) {
	root, err := os.OpenRoot(hostRoot)
	if err != nil {
		return Usage{}, err
	}
	defer root.Close()
	// Root confines symlink traversal to the selected host view.
	name := strings.TrimPrefix(mount.Point, "/")
	if name == "" {
		name = "."
	}
	file, err := openCapacityPath(root, name)
	if err != nil {
		return Usage{}, err
	}
	defer file.Close()
	if err := verifyCapacityMount(file, mount.ID); err != nil {
		return Usage{}, err
	}
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
			return Usage{}, fmt.Errorf("statfs: %w: %v", ErrUnsupported, err)
		}
		return Usage{}, err
	}
	if stat.Blocks == 0 {
		return Usage{}, fmt.Errorf("filesystem reports no capacity accounting: %w", ErrUnsupported)
	}
	blockSize := fragmentSize(&stat)
	if blockSize == 0 || stat.Bfree > stat.Blocks || stat.Bavail > stat.Bfree {
		return Usage{}, parseError(fmt.Errorf("invalid statfs block counts"))
	}
	if uint64(stat.Blocks) > math.MaxUint64/blockSize || uint64(stat.Bavail) > math.MaxUint64/blockSize {
		return Usage{}, parseError(fmt.Errorf("statfs size overflow"))
	}
	return Usage{Total: uint64(stat.Blocks) * blockSize, Free: uint64(stat.Bavail) * blockSize, Used: uint64(stat.Blocks-stat.Bfree) * blockSize}, nil
}
