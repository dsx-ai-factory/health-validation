// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/disk"
)

func TestReadKernelNamespace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proc/self/ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("mnt:[4026531840]", filepath.Join(root, "proc/self/ns/mnt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), []byte("9 1 8:0 / / rw - ext4 /dev/sda rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bootPath := filepath.Join(root, "proc/sys/kernel/random")
	if err := os.MkdirAll(bootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bootPath, "boot_id"), []byte("boot-A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mounts, failures, err := ReadMounts(os.DirFS(root), "")
	if err != nil || len(failures) != 0 || len(mounts) != 1 || mounts[0].Namespace != "boot-A:mnt:[4026531840]" {
		t.Fatalf("%+v %+v %v", mounts, failures, err)
	}
}

func TestStackedMountsAndFilterNeverProbe(t *testing.T) {
	files := fstest.MapFS{"proc/self/mountinfo": {Data: []byte("10 1 8:0 / /stack rw - ext4 /dev/sda rw\n11 10 0:3 / /stack rw - fuse.sshfs remote rw\n12 1 8:1 / /excluded rw - xfs /dev/sdb rw\n")}}
	s := New(files, WithNamespace("mnt:[1]"), WithMountFilter(func(m Mount) bool { return m.Point != "/excluded" }), WithStatfs(func(string) (Usage, error) {
		t.Error("statfs called for unsafe or filtered mount")
		return Usage{}, nil
	}))
	readings, err := s.Read(context.Background(), collect.Signals{signals.MountPoint, signals.TotalBytes})
	if err != nil || len(readings) != 4 {
		t.Fatalf("%+v %v", readings, err)
	}
	for _, r := range readings {
		if r.Signal == signals.TotalBytes && r.NoValue.GetReason() != observation.NoValueReasonNotSupported {
			t.Fatalf("unsafe capacity: %+v", r)
		}
	}
}

func TestMalformedMountDoesNotEraseValidMetadata(t *testing.T) {
	files := fstest.MapFS{"proc/self/mountinfo": {Data: []byte("10 1 8:0 / /safe rw - ext4 /dev/sda rw\n11 malformed\n")}}
	readings, err := New(files, WithNamespace("mnt:[1]")).Read(context.Background(), collect.Signals{signals.MountPoint})
	if err != nil || len(readings) != 2 {
		t.Fatalf("%+v %v", readings, err)
	}
	var valid, failed bool
	for _, r := range readings {
		valid = valid || r.Value.GetStringValue() == "/safe"
		failed = failed || r.Failure.GetCategory() == observation.CollectionErrorCategoryParseError
	}
	if !valid || !failed {
		t.Fatalf("%+v", readings)
	}
}
