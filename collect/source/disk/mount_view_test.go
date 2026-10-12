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

func TestAlternateRootRequiresExplicitMountView(t *testing.T) {
	files := fstest.MapFS{"proc/self/mountinfo": {Data: []byte("10 1 8:0 / / rw - ext4 /dev/sda rw\n")}}
	readings, err := New(files, WithNamespace("container"), WithHostRoot(t.TempDir())).Read(context.Background(), collect.Signals{signals.TotalBytes})
	if err != nil || len(readings) != 1 || readings[0].Failure.GetCategory() != observation.CollectionErrorCategoryUnsupported {
		t.Fatalf("mismatched root accepted: %+v %v", readings, err)
	}
}

func TestAlternateRootUsesExplicitProcessMountInfo(t *testing.T) {
	root := t.TempDir()
	files := fstest.MapFS{
		"proc/self/mountinfo": {Data: []byte("11 1 0:9 / /container-only rw - tmpfs tmpfs rw\n")},
		"proc/42/mountinfo":   {Data: []byte("10 1 8:0 / / rw - ext4 /dev/sda rw\n")},
	}
	readings, err := New(files, WithNamespace("host"), WithHostRoot(root), WithMountPID(42)).Read(context.Background(), collect.Signals{signals.MountPoint, signals.TotalBytes})
	if err != nil || len(readings) != 2 || readings[0].Value.GetStringValue() != "/" || readings[1].Failure != nil || readings[1].Value.GetUintValue() == 0 {
		t.Fatalf("explicit host view: %+v %v", readings, err)
	}
	if readings[0].Target.GetMount().MountId != 10 {
		t.Fatal(readings[0])
	}
}

func TestExplicitProcessDefaultNamespaceIsBootScoped(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"proc/42/ns", "proc/sys/kernel/random"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("mnt:[99]", filepath.Join(root, "proc/42/ns/mnt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc/sys/kernel/random/boot_id"), []byte("boot-A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proc/42/mountinfo"), []byte("10 1 8:0 / / rw - ext4 /dev/sda rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mounts, failures, err := ReadMounts(os.DirFS(root), "", 42)
	if err != nil || len(failures) != 0 || len(mounts) != 1 || mounts[0].Namespace != "boot-A:mnt:[99]" {
		t.Fatalf("%+v %+v %v", mounts, failures, err)
	}
}
