// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	signals "github.com/dsx-ai-factory/health-validation/collect/signals/disk"
)

func TestMountInfoEscapesAndPartialFailure(t *testing.T) {
	mounts, failures := ParseMountInfo(strings.NewReader("25 1 8:1 /sub\\040dir /mnt\\040space rw,nosuid shared:7 - ext4 /dev/disk\\134name rw,errors=remount-ro\n26 25 broken / /bad rw - ext4 /dev/sda rw\n27 25 0:44 / /fuse rw - fuse.sshfs remote\\040source rw\n"), "mnt:[5]")
	if len(mounts) != 2 || len(failures) != 1 || failures[0].ID != 26 {
		t.Fatalf("mounts=%+v failures=%+v", mounts, failures)
	}
	m := mounts[0]
	if m.Point != "/mnt space" || m.Root != "/sub dir" || m.Source != "/dev/disk\\name" || m.OptionalFields != "shared:7" || m.SuperOptions != "rw,errors=remount-ro" || m.Namespace != "mnt:[5]" {
		t.Fatalf("escaped mount: %+v", m)
	}
}

func TestNestedBlockInventory(t *testing.T) {
	root := t.TempDir()
	put := func(name, text string) {
		t.Helper()
		name = filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("sys/devices/block/sda/dev", "8:0")
	put("sys/devices/block/sda/size", "18446744073709551615")
	put("sys/devices/block/sda/device/serial", "real-serial\n")
	put("sys/devices/block/sda/wwid", "naa.123")
	put("sys/devices/block/sda/device/model", "model-A")
	put("sys/devices/block/sda/device/vendor", "vendor-A")
	put("sys/devices/block/sda/device/rev", "rev-A")
	put("sys/devices/block/sda/queue/rotational", "1")
	put("sys/devices/block/sda/sda1/dev", "8:1")
	put("sys/devices/block/sda/sda1/partition", "1")
	put("sys/devices/block/sda/sda1/size", "2048")
	put("sys/devices/virtual/block/dm-0/dev", "253:0")
	put("sys/devices/virtual/block/dm-0/size", "1024")
	if err := os.MkdirAll(filepath.Join(root, "sys/class/block"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, dest := range map[string]string{"sda": "../../devices/block/sda", "sda1": "../../devices/block/sda/sda1", "dm-0": "../../devices/virtual/block/dm-0"} {
		if err := os.Symlink(dest, filepath.Join(root, "sys/class/block", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "sys/devices/virtual/block/dm-0/slaves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../block/sda/sda1", filepath.Join(root, "sys/devices/virtual/block/dm-0/slaves/sda1")); err != nil {
		t.Fatal(err)
	}
	s := New(os.DirFS(root))
	readings, err := s.Read(context.Background(), collect.Signals{signals.Name, signals.Parents, signals.SizeBytes, signals.Serial, signals.WWN, signals.Model, signals.Vendor, signals.Revision, signals.Rotational, signals.Major, signals.Minor, signals.Type})
	if err != nil {
		t.Fatal(err)
	}
	var partitionParent, mapperParent, overflow, partial bool
	for _, r := range readings {
		dev := r.Target.GetBlockDevice()
		if dev == nil {
			t.Fatalf("missing block identity: %+v", r)
		}
		if r.Signal == signals.Parents && dev.Major == 8 && dev.Minor == 1 {
			partitionParent = r.Value.GetStringValue() == "8:0"
		}
		if r.Signal == signals.Parents && dev.Major == 253 {
			mapperParent = r.Value.GetStringValue() == "8:1"
		}
		if r.Signal == signals.SizeBytes && dev.Major == 8 && dev.Minor == 0 {
			overflow = r.Failure.GetCategory() == observation.CollectionErrorCategoryParseError
		}
		if r.Signal == signals.Serial && dev.Major == 253 {
			partial = r.Failure != nil
		}
		if dev.Major == 8 && dev.Minor == 1 {
			expected := map[collect.Signal]string{signals.Serial: "real-serial", signals.WWN: "naa.123", signals.Model: "model-A", signals.Vendor: "vendor-A", signals.Revision: "rev-A", signals.Type: "partition"}
			if value, ok := expected[r.Signal]; ok && (r.Failure != nil || r.Value.GetStringValue() != value) {
				t.Fatalf("inherited field %s: %+v", r.Signal.ID(), r)
			}
			if r.Signal == signals.Rotational && !r.Value.GetBoolValue() {
				t.Fatal("rotational was not inherited")
			}
		}
	}
	if !partitionParent || !mapperParent || !overflow || !partial {
		t.Fatalf("parent/overflow/partial flags: %v %v %v %v; readings=%+v", partitionParent, mapperParent, overflow, partial, readings)
	}
}

func TestMountCapacitySafeSelectionAndFailure(t *testing.T) {
	files := fstest.MapFS{"proc/self/mountinfo": {Data: []byte("1 0 8:0 / /local rw - ext4 /dev/sda rw\n2 0 0:3 / /network rw - nfs host:/ rw\n3 0 8:1 / /broken rw - xfs /dev/sdb rw\n")}}
	var calls []string
	s := New(files, WithNamespace("mnt:[6]"), WithStatfs(func(point string) (Usage, error) {
		calls = append(calls, point)
		if point == "/broken" {
			return Usage{}, fs.ErrPermission
		}
		return Usage{Total: 100, Free: 30, Used: 60}, nil
	}))
	readings, err := s.Read(context.Background(), collect.Signals{signals.TotalBytes, signals.FreeBytes, signals.UsedBytes, signals.MountDevice, signals.MountOptions})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "/local" || calls[1] != "/broken" {
		t.Fatalf("unsafe or repeated statfs calls: %v", calls)
	}
	var failureCount, skipped int
	for _, r := range readings {
		if r.Failure != nil {
			if r.Failure.Category != observation.CollectionErrorCategoryPermissionDenied {
				t.Fatal(r.Failure)
			}
			failureCount++
		}
		if r.NoValue != nil {
			skipped++
		}
	}
	if failureCount != 3 || skipped != 3 {
		t.Fatalf("failures=%d skipped=%d", failureCount, skipped)
	}
	calls = nil
	if _, err := s.Read(context.Background(), collect.Signals{signals.MountOptions}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatal("metadata called statfs")
	}
}

func TestStatfsTimeoutRejectsReentry(t *testing.T) {
	files := fstest.MapFS{"proc/self/mountinfo": {Data: []byte("1 0 8:0 / /local rw - ext4 /dev/sda rw\n")}}
	blocked := make(chan struct{})
	finished := make(chan struct{})
	var calls atomic.Int32
	s := New(files, WithNamespace("mnt:[6]"), WithStatfsTimeout(5*time.Millisecond), WithStatfs(func(string) (Usage, error) { calls.Add(1); <-blocked; close(finished); return Usage{}, nil }))
	first, err := s.Read(context.Background(), collect.Signals{signals.TotalBytes})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Failure.GetCategory() != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("first=%+v", first)
	}
	second, err := s.Read(context.Background(), collect.Signals{signals.TotalBytes})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Failure.GetCategory() != observation.CollectionErrorCategoryUnavailable || calls.Load() != 1 {
		t.Fatalf("second=%+v calls=%d", second, calls.Load())
	}
	close(blocked)
	<-finished
}

func TestEmptySelectionNoIO(t *testing.T) {
	readings, err := New(fstest.MapFS{}).Read(context.Background(), collect.Signals{})
	if err != nil || len(readings) != 0 {
		t.Fatalf("%v %v", readings, err)
	}
}

func TestNonUTF8Metadata(t *testing.T) {
	fixture := fstest.MapFS{
		"proc/self/mountinfo":              {Data: []byte("42 1 8:1 / /bad\xff rw - ext4 /dev/sda rw\n")},
		"sys/class/block/sda/dev":          {Data: []byte("8:0")},
		"sys/class/block/sda/device/model": {Data: []byte{'m', 0xff}},
	}
	readings, err := New(fixture, WithNamespace("fixture")).Read(context.Background(), collect.Signals{signals.Model, signals.MountPoint, signals.MountFSType})
	if err != nil {
		t.Fatal(err)
	}
	for _, reading := range readings {
		if reading.Signal == signals.MountFSType {
			if reading.Value.GetStringValue() != "ext4" || reading.Failure != nil {
				t.Fatalf("healthy field lost: %+v", reading)
			}
		} else if reading.Value != nil || reading.Failure.GetCategory() != observation.CollectionErrorCategoryParseError {
			t.Fatalf("unencodable value retained: %+v", reading)
		}
	}
}
