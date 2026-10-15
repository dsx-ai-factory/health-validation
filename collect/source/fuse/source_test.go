// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fuse

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/fuse"
)

func TestQueuesPartialFailureAndCorrelation(t *testing.T) {
	files := fstest.MapFS{
		"proc/sys/kernel/random/boot_id":                  {Data: []byte("boot-A\n")},
		"proc/self/mountinfo":                             {Data: []byte("10 1 0:53 / /first rw - fuse.sshfs shared-source rw\n11 1 0:54 / /second rw - fuse.sshfs shared-source rw\n12 10 0:53 /sub /bind\\040mount rw - fuse.sshfs shared-source rw\n13 1 8:53 / /wrong rw - ext4 /dev/sda rw\n")},
		"sys/fs/fuse/connections/53/waiting":              {Data: []byte("18446744073709551615")},
		"sys/fs/fuse/connections/53/max_background":       {Data: []byte("0")},
		"sys/fs/fuse/connections/53/congestion_threshold": {Data: []byte("invalid")},
		"sys/fs/fuse/connections/54/waiting":              {Data: []byte("7")},
		"sys/fs/fuse/connections/54/max_background":       {Data: []byte("12")},
		"sys/fs/fuse/connections/54/congestion_threshold": {Data: []byte("9")},
	}
	readings, err := New(files, WithNamespace("mnt:[5]")).Read(context.Background(), signals.All())
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 12 {
		t.Fatalf("deduplicated by source or lost readings: %d", len(readings))
	}
	var queues, zero, partial, correlation bool
	for _, r := range readings {
		ref := r.Target.GetFuseConnection()
		if ref == nil || ref.BootId != "boot-A" {
			t.Fatalf("identity: %+v", r.Target)
		}
		if ref.ConnectionId != 53 {
			continue
		}
		switch r.Signal {
		case signals.Waiting:
			queues = r.Value.GetUintValue() == ^uint64(0)
		case signals.MaxBackground:
			zero = r.Value.GetUintValue() == 0 && r.Failure == nil
		case signals.CongestionThreshold:
			partial = r.Failure.GetCategory() == observation.CollectionErrorCategoryParseError
		case signals.Mounts:
			value := r.Value.GetStringValue()
			correlation = strings.Contains(value, "mnt:[5]/10") && strings.Contains(value, "mnt:[5]/12") && !strings.Contains(value, "/13")
		}
	}
	if !queues || !zero || !partial || !correlation {
		t.Fatalf("queues/zero/partial/correlation=%v %v %v %v", queues, zero, partial, correlation)
	}
}

type trackedFS struct {
	fs.FS
	opened *[]string
}

func (f trackedFS) Open(name string) (fs.File, error) {
	*f.opened = append(*f.opened, name)
	return f.FS.Open(name)
}

func TestSelectionAvoidsUnrelatedMountAndQueueReads(t *testing.T) {
	var opened []string
	files := trackedFS{FS: fstest.MapFS{"proc/sys/kernel/random/boot_id": {Data: []byte("boot-A")}, "sys/fs/fuse/connections/53/waiting": {Data: []byte("1")}}, opened: &opened}
	readings, err := New(files).Read(context.Background(), collect.Signals{signals.Waiting})
	if err != nil || len(readings) != 1 || readings[0].Value.GetUintValue() != 1 {
		t.Fatalf("%+v %v", readings, err)
	}
	for _, name := range opened {
		if strings.Contains(name, "mountinfo") || strings.Contains(name, "background") || strings.Contains(name, "threshold") {
			t.Fatalf("unrelated read: %v", opened)
		}
	}
	opened = nil
	if _, err := New(files).Read(context.Background(), collect.Signals{}); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 0 {
		t.Fatalf("empty selection accessed files: %v", opened)
	}
}

func TestAbsentFUSEIsNoTargets(t *testing.T) {
	readings, err := New(fstest.MapFS{}).Read(context.Background(), signals.All())
	if err != nil || len(readings) != len(signals.All()) {
		t.Fatalf("%+v %v", readings, err)
	}
	for _, r := range readings {
		if r.Target != nil || r.NoValue.GetReason() != observation.NoValueReasonNoTargets {
			t.Fatalf("%+v", r)
		}
	}
}
