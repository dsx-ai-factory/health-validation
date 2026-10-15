// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"strings"
	"testing"
)

func TestDuplicateMountIDsRejectEveryConflictingRecord(t *testing.T) {
	input := "10 1 8:0 / /first rw - ext4 /dev/sda rw\n10 1 8:1 / /second rw - xfs /dev/sdb rw\n11 1 8:2 / /valid rw - ext4 /dev/sdc rw\n"
	mounts, failures := ParseMountInfo(strings.NewReader(input), "scope")
	if len(mounts) != 1 || mounts[0].ID != 11 || len(failures) != 1 || failures[0].ID != 10 {
		t.Fatalf("mounts=%+v failures=%+v", mounts, failures)
	}
}

func TestMalformedDuplicateCannotLeaveSuccessfulIdentity(t *testing.T) {
	input := "10 1 8:0 / /first rw - ext4 /dev/sda rw\n10 malformed\n"
	mounts, failures := ParseMountInfo(strings.NewReader(input), "scope")
	if len(mounts) != 0 || len(failures) != 1 || failures[0].ID != 10 {
		t.Fatalf("mounts=%+v failures=%+v", mounts, failures)
	}
}

func TestMalformedMountBoundsHaveNoFabricatedTarget(t *testing.T) {
	for _, input := range []string{
		"18446744073709551616 1 8:0 / / rw - ext4 /dev/sda rw\n",
		"0 1 8:0 / / rw - ext4 /dev/sda rw\n",
		"+1 1 8:0 / / rw - ext4 /dev/sda rw\n",
	} {
		mounts, failures := ParseMountInfo(strings.NewReader(input), "scope")
		if len(mounts) != 0 || len(failures) != 1 || failures[0].ID != 0 {
			t.Fatalf("input=%q mounts=%+v failures=%+v", input, mounts, failures)
		}
	}
}
