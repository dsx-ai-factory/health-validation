// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/disk"
	"golang.org/x/sys/unix"
)

func capacityMountID(t *testing.T, root string) uint64 {
	t.Helper()
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	file, err := openCapacityPath(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var stat unix.Statx_t
	err = unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_STATX_DONT_SYNC, unix.STATX_MNT_ID, &stat)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) || err == nil && stat.Mask&unix.STATX_MNT_ID == 0 {
		t.Skip("kernel does not expose descriptor mount identity")
	}
	if err != nil {
		t.Fatal(err)
	}
	return stat.Mnt_id
}

func TestNativeUsageRequiresOpenedMountIdentity(t *testing.T) {
	root := t.TempDir()
	id := capacityMountID(t, root)
	usage, err := nativeUsage(root, Mount{Point: "/", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Total == 0 || usage.Free > usage.Total || usage.Used > usage.Total {
		t.Fatalf("invalid native capacity: %+v", usage)
	}
	usage, err = nativeUsage(root, Mount{Point: "/", ID: id + 1})
	if !errors.Is(err, os.ErrNotExist) || usage != (Usage{}) {
		t.Fatalf("different mount identity returned capacity: %+v %v", usage, err)
	}
}

func TestMountReplacementDoesNotAttachCapacityToEnumeratedTarget(t *testing.T) {
	root := t.TempDir()
	openedID := capacityMountID(t, root)
	// Model a snapshot taken before replacement: its ID no longer names the
	// mount opened at the same point. Device/path comparisons cannot establish
	// that these are the same mount; only the descriptor's mount ID can.
	enumeratedID := openedID + 1
	files := fstest.MapFS{
		"proc/42/mountinfo": {Data: []byte(fmt.Sprintf("%d 1 0:1 / / rw - tmpfs tmpfs rw\n", enumeratedID))},
	}
	readings, err := New(files, WithNamespace("snapshot"), WithHostRoot(root), WithMountPID(42)).Read(context.Background(), collect.Signals{signals.MountPoint, signals.TotalBytes, signals.FreeBytes, signals.UsedBytes})
	if err != nil || len(readings) != 4 {
		t.Fatalf("readings=%+v err=%v", readings, err)
	}
	for _, reading := range readings {
		if reading.Target.GetMount().MountId != enumeratedID {
			t.Fatalf("lost snapshot identity: %+v", reading)
		}
		if reading.Signal == signals.MountPoint {
			if reading.Value.GetStringValue() != "/" || reading.Failure != nil {
				t.Fatalf("lost snapshot metadata: %+v", reading)
			}
			continue
		}
		if reading.Value != nil || reading.Failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Fatalf("replacement capacity attributed to snapshot: %+v", reading)
		}
	}
}
