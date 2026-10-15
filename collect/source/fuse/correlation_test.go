// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fuse

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/fuse"
)

func TestInvalidConnectionIDsKeepHealthyQueue(t *testing.T) {
	files := fstest.MapFS{
		"proc/sys/kernel/random/boot_id":     {Data: []byte("boot-A")},
		"sys/fs/fuse/connections/53/waiting": {Data: []byte("9")},
	}
	for _, name := range []string{"0", "+1", "18446744073709551616"} {
		files["sys/fs/fuse/connections/"+name] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	}
	readings, err := New(files).Read(context.Background(), collect.Signals{signals.Waiting})
	if err != nil || len(readings) != 4 {
		t.Fatalf("%+v %v", readings, err)
	}
	var valid, invalid int
	for _, reading := range readings {
		if reading.Failure != nil {
			if reading.Target != nil || reading.Failure.Category != observation.CollectionErrorCategoryParseError {
				t.Fatalf("fabricated connection target: %+v", reading)
			}
			invalid++
		} else {
			if reading.Target.GetFuseConnection().ConnectionId != 53 || reading.Value.GetUintValue() != 9 {
				t.Fatalf("healthy queue lost: %+v", reading)
			}
			valid++
		}
	}
	if valid != 1 || invalid != 3 {
		t.Fatalf("valid=%d invalid=%d", valid, invalid)
	}
}

func TestBrokenMountInfoDoesNotEraseQueue(t *testing.T) {
	files := fstest.MapFS{
		"proc/sys/kernel/random/boot_id":     {Data: []byte("boot-A")},
		"sys/fs/fuse/connections/53/waiting": {Data: []byte("9")},
		"proc/self/mountinfo":                {Data: []byte("10 malformed\n")},
	}
	readings, err := New(files, WithNamespace("mnt:[1]")).Read(context.Background(), collect.Signals{signals.Waiting, signals.Mounts})
	if err != nil || len(readings) != 2 {
		t.Fatalf("%+v %v", readings, err)
	}
	if readings[0].Value.GetUintValue() != 9 || readings[1].Failure.GetCategory() != observation.CollectionErrorCategoryParseError {
		t.Fatalf("%+v", readings)
	}
}

func TestCorrelationSelectionDoesNotReadQueues(t *testing.T) {
	files := fstest.MapFS{
		"proc/sys/kernel/random/boot_id": {Data: []byte("boot-A")},
		"sys/fs/fuse/connections/53":     {Mode: 0o755 | fs.ModeDir},
		"proc/self/mountinfo":            {Data: []byte("10 1 0:53 / /mount rw - fuse.testfs source rw\n")},
	}
	readings, err := New(files, WithNamespace("mnt:[1]")).Read(context.Background(), collect.Signals{signals.Sources})
	if err != nil || len(readings) != 1 || readings[0].Failure != nil || readings[0].Value.GetStringValue() == "" {
		t.Fatalf("%+v %v", readings, err)
	}
}
