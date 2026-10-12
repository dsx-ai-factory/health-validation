// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pci

import (
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	pcisignals "github.com/dsx-ai-factory/health-validation/collect/signals/pci"
)

func TestParseACS(t *testing.T) {
	config := make([]byte, 4096)
	binary.LittleEndian.PutUint32(config[0x100:], 0x18000001|1<<16)
	binary.LittleEndian.PutUint32(config[0x180:], 0x0001000d)
	binary.LittleEndian.PutUint16(config[0x184:], 0x807f)
	binary.LittleEndian.PutUint16(config[0x186:], 0x8045)
	acs, err := ParseACS(config)
	if err != nil || acs == nil || acs.Offset != 0x180 || acs.Capability != 0x807f || acs.Control != 0x8045 {
		t.Fatalf("ACS = %+v, %v", acs, err)
	}
	for name, fixture := range map[string][]byte{"truncated_header": config[:0x182], "truncated_registers": config[:0x187]} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseACS(fixture); !errors.Is(err, ErrMalformed) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	cycle := make([]byte, 4096)
	binary.LittleEndian.PutUint32(cycle[0x100:], 0x10010001)
	if _, err := ParseACS(cycle); !errors.Is(err, ErrMalformed) {
		t.Fatalf("cyclic error = %v", err)
	}
	binary.LittleEndian.PutUint32(cycle[0x100:], 0x1001000d)
	if _, err := ParseACS(cycle); !errors.Is(err, ErrMalformed) {
		t.Fatalf("cyclic ACS error = %v", err)
	}
	emptyLink := make([]byte, 4096)
	binary.LittleEndian.PutUint32(emptyLink[0x100:], 0x18010001)
	if _, err := ParseACS(emptyLink); !errors.Is(err, ErrMalformed) {
		t.Fatalf("empty linked header error = %v", err)
	}
	misaligned := make([]byte, 4096)
	binary.LittleEndian.PutUint32(misaligned[0x100:], 0x10210001)
	if _, err := ParseACS(misaligned); !errors.Is(err, ErrMalformed) {
		t.Fatalf("alignment error = %v", err)
	}
	if acs, err := ParseACS(make([]byte, 4096)); acs != nil || err != nil {
		t.Fatalf("absent ACS = %+v, %v", acs, err)
	}
	for _, size := range []int{64, 128, 256} {
		if _, err := ParseACS(make([]byte, size)); !errors.Is(err, ErrLimitedConfig) || Failure(err).Category != observation.CollectionErrorCategoryUnavailable {
			t.Fatalf("limited %d-byte config error = %v", size, err)
		}
	}
}

type deniedConfig struct{ fs.FS }

func (d deniedConfig) Open(name string) (fs.File, error) {
	if name == Root+"/0000:01:00.0/config" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.FS.Open(name)
}

func TestReadRetainsEnumerationAndGPUWithoutConfig(t *testing.T) {
	fixture := fstest.MapFS{
		Root + "/0000:01:00.0/vendor": {Data: []byte("0x10de\n")},
		Root + "/0000:01:00.0/device": {Data: []byte("0x2330\n")},
		Root + "/0000:01:00.0/class":  {Data: []byte("0x030200\n")},
		Root + "/0000:01:00.0/config": {Data: make([]byte, 4096)},
		Root + "/0000:02:00.0/vendor": {Data: []byte("broken\n")},
		Root + "/0000:02:00.0/device": {Data: []byte("0x1017\n")},
		Root + "/0000:02:00.0/class":  {Data: []byte("0x020000\n")},
	}
	readings, err := New(deniedConfig{fixture}).Read(context.Background(), pcisignals.All())
	if err != nil || len(readings) != 2*len(pcisignals.All()) {
		t.Fatalf("readings = %d, %v", len(readings), err)
	}
	for _, reading := range readings {
		address := reading.Target.GetPci().GetAddress()
		switch {
		case address == "0000:01:00.0" && reading.Signal.ID() == pcisignals.GPUPresent.ID():
			if reading.Value == nil || !reading.Value.GetBoolValue() {
				t.Fatalf("GPU presence lost: %+v", reading)
			}
		case address == "0000:01:00.0" && reading.Signal.ID() == pcisignals.ACSControl.ID():
			if reading.Failure == nil || reading.Failure.Category != observation.CollectionErrorCategoryPermissionDenied {
				t.Fatalf("permission = %+v", reading)
			}
		case address == "0000:02:00.0" && reading.Signal.ID() == pcisignals.ACSControl.ID():
			if reading.Failure == nil || reading.Failure.Category != observation.CollectionErrorCategoryUnavailable {
				t.Fatalf("missing config = %+v", reading)
			}
		case address == "0000:02:00.0" && reading.Signal.ID() == pcisignals.Vendor.ID():
			if reading.Failure == nil || reading.Failure.Category != observation.CollectionErrorCategoryParseError {
				t.Fatalf("parse = %+v", reading)
			}
		}
	}
}

func TestNoTargets(t *testing.T) {
	fixture := fstest.MapFS{Root: {Mode: fs.ModeDir | 0o755}}
	readings, err := New(fixture).Read(context.Background(), collect.Signals{pcisignals.Address})
	if err != nil || len(readings) != 1 || readings[0].Target != nil || readings[0].NoValue.GetReason() != observation.NoValueReasonNoTargets {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
}

func TestSysfsDeviceDriverAndModuleLinks(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"sys/bus/pci/devices", "sys/devices/pci0000:00/0000:01:00.0", "sys/bus/pci/drivers/nvidia", "sys/module/nvidia"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sys/devices/pci0000:00/0000:01:00.0/class"), []byte("0x030200\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sys/devices/pci0000:00/0000:01:00.0/vendor"), []byte("0x10de\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ name, target string }{
		{"sys/bus/pci/devices/0000:01:00.0", "../../../devices/pci0000:00/0000:01:00.0"},
		{"sys/devices/pci0000:00/0000:01:00.0/driver", "../../../bus/pci/drivers/nvidia"},
		{"sys/bus/pci/drivers/nvidia/module", "../../../../module/nvidia"},
	} {
		if err := os.Symlink(link.target, filepath.Join(root, link.name)); err != nil {
			t.Fatal(err)
		}
	}
	readings, err := New(os.DirFS(root)).Read(context.Background(), collect.Signals{pcisignals.Driver, pcisignals.Module, pcisignals.GPUPresent})
	if err != nil || len(readings) != 3 {
		t.Fatalf("readings = %+v, %v", readings, err)
	}
	for _, reading := range readings {
		if reading.Failure != nil {
			t.Fatalf("link failure = %+v", reading)
		}
		if reading.Signal.ID() == pcisignals.GPUPresent.ID() {
			if !reading.Value.GetBoolValue() {
				t.Fatal("GPU not detected")
			}
		} else if reading.Value.GetStringValue() != "nvidia" {
			t.Fatalf("binding = %+v", reading)
		}
	}
}
