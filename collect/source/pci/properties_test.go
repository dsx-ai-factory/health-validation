// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pci

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
)

// readProperty reads the single signal from files and fails the test unless
// exactly one reading is returned.
func readProperty(t *testing.T, files fs.FS, signal collect.Signal) Reading {
	t.Helper()
	readings, err := New(files).Read(context.Background(), collect.Signals{signal})
	if err != nil || len(readings) != 1 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	return readings[0]
}

// propertyFixture builds a single-device sysfs fixture with field set to value.
func propertyFixture(field, value string) fstest.MapFS {
	return fstest.MapFS{Root + "/0000:01:00.0": {Mode: fs.ModeDir | 0o755}, Root + "/0000:01:00.0/" + field: {Data: []byte(value)}}
}

// signed wraps n as an int64 observation value.
func signed(n int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: n}}
}

// double wraps n as a double observation value.
func double(n float64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: n}}
}

// TestLinkValues checks that link speed, width, and generation signals parse
// their sysfs attributes into the expected observation values.
func TestLinkValues(t *testing.T) {
	for _, test := range []struct {
		signal collect.Signal
		want   *observation.Value
		field  string
		raw    string
	}{
		{field: "current_link_speed", raw: "16.0 GT/s PCIe\n", signal: pcisignals.LinkSpeed, want: double(16)},
		{field: "max_link_speed", raw: "64.0 GT/s PCIe\n", signal: pcisignals.MaxLinkSpeed, want: double(64)},
		{field: "current_link_speed", raw: "16.0 GT/s PCIe\n", signal: pcisignals.LinkGeneration, want: number(4)},
		{field: "max_link_speed", raw: "64.0 GT/s PCIe\n", signal: pcisignals.MaxLinkGeneration, want: number(6)},
		{field: "current_link_width", raw: "16\n", signal: pcisignals.LinkWidth, want: signed(16)},
		{field: "max_link_width", raw: "32\n", signal: pcisignals.MaxLinkWidth, want: signed(32)},
		{field: "current_link_width", raw: "-1\n", signal: pcisignals.LinkWidth, want: signed(-1)},
	} {
		t.Run(test.signal.ID()+"/"+strings.TrimSpace(test.raw), func(t *testing.T) {
			r := readProperty(t, propertyFixture(test.field, test.raw), test.signal)
			if r.Failure != nil || r.NoValue != nil || r.Value == nil || !reflect.DeepEqual(r.Value.Kind, test.want.Kind) {
				t.Fatalf("reading = %+v, want %v", r, test.want)
			}
			if r.Target.GetPci().GetAddress() != "0000:01:00.0" {
				t.Fatal(r.Target)
			}
		})
	}
}

// TestLinksUnsupported checks that missing link attributes and unknown speeds
// produce NotSupported no-value readings instead of errors.
func TestLinksUnsupported(t *testing.T) {
	fixture := fstest.MapFS{Root + "/0000:01:00.0": {Mode: fs.ModeDir | 0o755}}
	for _, signal := range pcisignals.All()[10:] {
		r := readProperty(t, fixture, signal)
		if r.Value != nil || r.Failure != nil || r.NoValue.GetReason() != observation.NoValueReasonNotSupported {
			t.Fatalf("reading = %+v", r)
		}
	}
	for _, raw := range []string{"Unknown", "Unknown speed"} {
		for _, signal := range []collect.Signal{pcisignals.LinkSpeed, pcisignals.LinkGeneration, pcisignals.MaxLinkSpeed, pcisignals.MaxLinkGeneration} {
			field := "current_link_speed"
			if signal == pcisignals.MaxLinkSpeed || signal == pcisignals.MaxLinkGeneration {
				field = "max_link_speed"
			}
			r := readProperty(t, propertyFixture(field, raw), signal)
			if r.Value != nil || r.Failure != nil || r.NoValue.GetReason() != observation.NoValueReasonNotSupported {
				t.Fatalf("unknown = %+v", r)
			}
		}
	}
}

// TestLinkValuesMalformed checks that malformed link attribute values produce
// parse-error failures referencing the attribute path.
func TestLinkValuesMalformed(t *testing.T) {
	for _, test := range []struct {
		signal collect.Signal
		field  string
		raw    string
	}{
		{field: "current_link_width", raw: "x16", signal: pcisignals.LinkWidth},
		{field: "max_link_width", raw: "-2", signal: pcisignals.MaxLinkWidth},
		{field: "current_link_speed", raw: "16.0 GB/s", signal: pcisignals.LinkSpeed},
		{field: "current_link_speed", raw: "NaN GT/s", signal: pcisignals.LinkGeneration},
		{field: "max_link_speed", raw: "+Inf GT/s", signal: pcisignals.MaxLinkSpeed},
		{field: "current_link_speed", raw: "16.0 GT/s garbage", signal: pcisignals.LinkSpeed},
		{field: "current_link_speed", raw: strings.Repeat("0", 129), signal: pcisignals.LinkSpeed},
	} {
		t.Run(test.signal.ID()+"/"+test.raw[:min(len(test.raw), 30)], func(t *testing.T) {
			r := readProperty(t, propertyFixture(test.field, test.raw), test.signal)
			if r.Value != nil || r.NoValue != nil || r.Failure.GetCategory() != observation.CollectionErrorCategoryParseError || r.Failure.Operation != r.Path {
				t.Fatalf("reading = %+v", r)
			}
		})
	}
}

// failingAttribute wraps an fs.FS and makes Open fail for a single named file.
type failingAttribute struct {
	fs.FS
	err  error
	name string
}

// Open returns f.err for f.name and otherwise delegates to the wrapped fs.FS.
func (f failingAttribute) Open(name string) (fs.File, error) {
	if name == f.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: f.err}
	}
	return f.FS.Open(name)
}

// TestLinkReadFailures checks that permission, not-exist, and other open
// errors on link attributes map to the corresponding failure categories.
func TestLinkReadFailures(t *testing.T) {
	for _, test := range []struct {
		err      error
		category observation.CollectionErrorCategory
	}{
		{fs.ErrPermission, observation.CollectionErrorCategoryPermissionDenied},
		{fs.ErrNotExist, observation.CollectionErrorCategoryUnavailable},
		{fs.ErrClosed, observation.CollectionErrorCategorySourceError},
	} {
		for _, attr := range []struct {
			signal     collect.Signal
			field, raw string
		}{
			{signal: pcisignals.LinkSpeed, field: "current_link_speed", raw: "16.0 GT/s PCIe"},
			{signal: pcisignals.LinkWidth, field: "current_link_width", raw: "16"},
		} {
			r := readProperty(t, failingAttribute{FS: propertyFixture(attr.field, attr.raw), name: Root + "/0000:01:00.0/" + attr.field, err: test.err}, attr.signal)
			if r.NoValue != nil || r.Value != nil || r.Failure.GetCategory() != test.category {
				t.Fatalf("failed read = %+v", r)
			}
		}
	}
}

// TestMultipleDeviceLinks checks that link readings across several devices are
// resolved independently, so one device's malformed value does not affect others.
func TestMultipleDeviceLinks(t *testing.T) {
	files := propertyFixture("current_link_speed", "16.0 GT/s PCIe")
	files[Root+"/0000:02:00.0/current_link_speed"] = &fstest.MapFile{Data: []byte("broken")}
	readings, err := New(files).Read(context.Background(), collect.Signals{pcisignals.LinkSpeed, pcisignals.LinkWidth})
	if err != nil || len(readings) != 4 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	for _, r := range readings {
		switch {
		case r.Signal == pcisignals.LinkWidth:
			if r.NoValue.GetReason() != observation.NoValueReasonNotSupported {
				t.Fatal(r)
			}
		case r.Target.GetPci().GetAddress() == "0000:01:00.0":
			if r.Value == nil || r.Value.GetDoubleValue() != 16 {
				t.Fatal(r)
			}
		default:
			if r.Failure.GetCategory() != observation.CollectionErrorCategoryParseError {
				t.Fatal(r)
			}
		}
	}
}

// TestLinkGenerations checks that every defined PCI generation speed maps to
// its generation number, while an unrecognized numeric speed does not.
func TestLinkGenerations(t *testing.T) {
	for i, speed := range []string{"2.5", "5.0", "8.0", "16.0", "32.0", "64.0", "128.0"} {
		for _, attr := range []struct {
			signal collect.Signal
			field  string
		}{{pcisignals.LinkGeneration, "current_link_speed"}, {pcisignals.MaxLinkGeneration, "max_link_speed"}} {
			r := readProperty(t, propertyFixture(attr.field, speed+" GT/s"), attr.signal)
			if r.Value == nil || r.Value.GetUintValue() != uint64(i+1) {
				t.Fatalf("generation %s: %+v", speed, r)
			}
		}
	}
	fixture := propertyFixture("current_link_speed", "12.0 GT/s PCIe")
	r := readProperty(t, fixture, pcisignals.LinkSpeed)
	if r.Value == nil || r.Value.GetDoubleValue() != 12 {
		t.Fatalf("numeric speed = %+v", r)
	}
	r = readProperty(t, fixture, pcisignals.LinkGeneration)
	if r.Value != nil || r.Failure != nil || r.NoValue.GetReason() != observation.NoValueReasonNotSupported {
		t.Fatalf("unknown generation = %+v", r)
	}
}

// TestMissingDeviceIsNotUnsupported checks that a dangling device symlink
// yields Unavailable failures rather than NotSupported no-value readings.
func TestMissingDeviceIsNotUnsupported(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, Root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/missing/device", filepath.Join(root, Root, "0000:01:00.0")); err != nil {
		t.Fatal(err)
	}
	readings, err := New(os.DirFS(root)).Read(context.Background(), pcisignals.All()[10:])
	if err != nil || len(readings) != 6 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	for _, r := range readings {
		if r.Value != nil || r.NoValue != nil || r.Failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Fatalf("missing device = %+v", r)
		}
	}
}
