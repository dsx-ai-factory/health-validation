// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fuse

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/fuse"
)

func TestFuseblkKernelDeviceIdentityAndExplicitMountPID(t *testing.T) {
	files := fstest.MapFS{
		"proc/sys/kernel/random/boot_id":          {Data: []byte("boot-A")},
		"proc/42/mountinfo":                       {Data: []byte("10 1 8:17 / /block rw - fuseblk /dev/sdb1 rw\n11 1 8:17 / /not-fuse rw - ext4 /dev/sdb1 rw\n")},
		"sys/fs/fuse/connections/8388625/waiting": {Data: []byte("9")},
	}
	readings, err := New(files, WithNamespace("host-scope"), WithMountPID(42)).Read(context.Background(), collect.Signals{signals.Waiting, signals.Mounts, signals.FSTypes, signals.Sources})
	if err != nil || len(readings) != 4 {
		t.Fatalf("%+v %v", readings, err)
	}
	if readings[0].Value.GetUintValue() != 9 {
		t.Fatal(readings[0])
	}
	for _, reading := range readings[1:] {
		value := reading.Value.GetStringValue()
		if reading.Failure != nil || !strings.Contains(value, "host-scope/10") || strings.Contains(value, "/11") {
			t.Fatalf("fuseblk correlation: %+v", reading)
		}
	}
}

func TestConnectionWithoutVisibleMountHasEmptyCorrelation(t *testing.T) {
	files := fstest.MapFS{
		"proc/sys/kernel/random/boot_id":     {Data: []byte("boot-A")},
		"proc/self/mountinfo":                {Data: []byte("10 1 8:53 / /ordinary rw - ext4 /dev/sda rw\n")},
		"sys/fs/fuse/connections/53/waiting": {Data: []byte("9")},
	}
	readings, err := New(files, WithNamespace("scope")).Read(context.Background(), collect.Signals{signals.Mounts, signals.FSTypes, signals.Sources})
	if err != nil || len(readings) != 3 {
		t.Fatalf("%+v %v", readings, err)
	}
	for _, reading := range readings {
		if reading.NoValue != nil || reading.Failure != nil || reading.Value == nil || reading.Value.GetStringValue() != "" || reading.Target.GetFuseConnection().ConnectionId != 53 {
			t.Fatalf("empty list lost connection: %+v", reading)
		}
	}
}
