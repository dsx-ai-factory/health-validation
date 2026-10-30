// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package gpuinventory

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

// basicDevice answers only identity and name reads. Its nil embedded device
// panics on any other call, so every test here also proves the reader skips
// enrichment.
type basicDevice struct {
	device.Device
	name    string
	uuid    string
	nameRet nvml.Return
	// nameRead fails the test when GetName is called.
	nameRead bool
}

func (d *basicDevice) UUID() string { return d.uuid }

func (d *basicDevice) GetName() (string, nvml.Return) {
	if d.nameRead {
		panic("GetName called after the architecture was recognized")
	}
	return d.name, d.nameRet
}

// fakeInstance is an nvmlsource.Instance over fixed enumeration records.
type fakeInstance struct {
	initErr error
	readErr error
	devices map[string]device.Device
	records []nvmlsource.InventoryDevice
	missing bool
}

func (i *fakeInstance) NVMLExists() bool                  { return !i.missing }
func (i *fakeInstance) Devices() map[string]device.Device { return i.devices }
func (i *fakeInstance) Refresh() error                    { return i.initErr }
func (*fakeInstance) Shutdown() error                     { return nil }
func (i *fakeInstance) InitError() error                  { return i.initErr }

func (i *fakeInstance) InventoryDevices() []nvmlsource.InventoryDevice { return i.records }

func (i *fakeInstance) Read(read func(map[string]device.Device) error) error {
	if i.readErr != nil {
		return i.readErr
	}
	return read(i.devices)
}

// withRecords builds an instance whose enumeration produced records.
func withRecords(records ...nvmlsource.InventoryDevice) *fakeInstance {
	inst := &fakeInstance{devices: make(map[string]device.Device), records: records}
	for _, record := range records {
		if record.Device != nil && record.UUIDReturn == nvml.SUCCESS {
			inst.devices[record.Device.UUID()] = record.Device
		}
	}
	return inst
}

func gpu(index uint32, name string) nvmlsource.InventoryDevice {
	return nvmlsource.InventoryDevice{SourceIndex: index, Device: &basicDevice{name: name, uuid: fmt.Sprintf("GPU-%d", index)}}
}

func readSnapshot(t *testing.T, inst nvmlsource.Instance) *observation.GPUInventorySnapshot {
	t.Helper()
	snapshot, err := NVML(inst).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	batch := &observation.ObservationBatch{Inventory: &observation.HardwareInventory{GpuSnapshots: []*observation.GPUInventorySnapshot{snapshot}}}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatalf("invalid snapshot: %v", err)
	}
	return snapshot
}

func TestNVMLReaderReadsBasicInventory(t *testing.T) {
	lost := nvmlsource.InventoryDevice{SourceIndex: 1, HandleReturn: nvml.ERROR_GPU_IS_LOST, Error: errors.New("device 1 handle: GPU is lost")}
	unwrapped := nvmlsource.InventoryDevice{SourceIndex: 2, Error: errors.New("bus ID lookup failed")}
	noUUID := gpu(3, "NVIDIA GB200")
	noUUID.UUIDReturn, noUUID.Error = nvml.ERROR_UNKNOWN, errors.New("device 3 UUID: Unknown Error")
	snapshot := readSnapshot(t, withRecords(gpu(0, "NVIDIA GB200"), lost, unwrapped, noUUID))

	entries := snapshot.GetInventory().GetEntries()
	if len(entries) != 4 {
		t.Fatalf("entries=%d, want every enumerated slot", len(entries))
	}
	for i, want := range []string{"", handleOperation, "NewDevice", "GetUUID"} {
		if entries[i].GetSourceIndex() != uint32(i) || entries[i].GetCollectionError().GetOperation() != want {
			t.Errorf("entry %d = index %d, failed %q; want index %d, failed %q", i, entries[i].GetSourceIndex(), entries[i].GetCollectionError().GetOperation(), i, want)
		}
	}
	if entries[0].GetGpu().GetUuid() != "GPU-0" || entries[0].GetGpu().GetModel() != "NVIDIA GB200" {
		t.Errorf("healthy entry = %v", entries[0].GetGpu())
	}
	// Legacy counts every slot whose handle loaded.
	if count, ok := Count(snapshot); !ok || count != 3 {
		t.Errorf("Count = %d, %v; want 3", count, ok)
	}
	if arch, err := Architecture(snapshot); err != nil || arch != "GB200" {
		t.Errorf("Architecture = %q, %v; want GB200", arch, err)
	}
}

// Models are read only until one is recognized, so a later GPU whose name
// read would hang cannot block discovery.
func TestNVMLReaderStopsAtRecognizedModel(t *testing.T) {
	later := gpu(1, "NVIDIA GB200")
	later.Device.(*basicDevice).nameRead = true
	snapshot := readSnapshot(t, withRecords(gpu(0, "NVIDIA GB300"), later))
	if snapshot.GetInventory().GetEntries()[1].GetGpu().Model != nil {
		t.Fatal("read a model after the architecture was recognized")
	}
	if arch, err := Architecture(snapshot); err != nil || arch != "GB300" {
		t.Errorf("Architecture = %q, %v; want GB300", arch, err)
	}
}

func TestNVMLReaderModelFallbacks(t *testing.T) {
	unreadable := gpu(0, "")
	unreadable.Device.(*basicDevice).nameRet = nvml.ERROR_UNKNOWN
	for _, tc := range []struct {
		name    string
		want    string
		records []nvmlsource.InventoryDevice
		failed  bool
	}{
		{name: "unreadable then recognized", records: []nvmlsource.InventoryDevice{unreadable, gpu(1, "NVIDIA H100 80GB HBM3")}, want: "H100"},
		{name: "unrecognized", records: []nvmlsource.InventoryDevice{gpu(0, "NVIDIA A10"), gpu(1, "NVIDIA A10")}, want: ArchitectureUnknown},
		{name: "no model readable", records: []nvmlsource.InventoryDevice{unreadable}, failed: true},
		{name: "zero GPUs", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arch, err := Architecture(readSnapshot(t, withRecords(tc.records...)))
			if (err != nil) != tc.failed || arch != tc.want {
				t.Fatalf("Architecture = %q, %v; want %q, failed=%v", arch, err, tc.want, tc.failed)
			}
		})
	}
}

func TestNVMLReaderFailures(t *testing.T) {
	for _, tc := range []struct {
		inst      *fakeInstance
		name      string
		operation string
		category  observation.CollectionErrorCategory
	}{
		{name: "NVML missing", inst: &fakeInstance{missing: true}, operation: "Init", category: observation.CollectionErrorCategoryUnsupported},
		{name: "NVML init failed", inst: &fakeInstance{missing: true, initErr: errors.New("init")}, operation: "Init", category: observation.CollectionErrorCategoryUnavailable},
		{name: "enumeration failed", inst: &fakeInstance{initErr: errors.New("get device count: Unknown Error")}, operation: "GetDevices", category: observation.CollectionErrorCategoryUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := readSnapshot(t, tc.inst)
			failure := snapshot.GetCollectionError()
			if failure.GetOperation() != tc.operation || failure.GetCategory() != tc.category {
				t.Fatalf("failure = %v, want %s %s", failure, tc.operation, tc.category)
			}
			if _, err := Architecture(snapshot); err == nil {
				t.Fatal("discovery succeeded without an inventory")
			}
			if _, ok := Count(snapshot); ok {
				t.Fatal("counted GPUs without an inventory")
			}
		})
	}
	t.Run("read error", func(t *testing.T) {
		inst := withRecords(gpu(0, "NVIDIA GB200"))
		inst.readErr = nvmlsource.ErrInUse
		if _, err := NVML(inst).Read(context.Background()); !errors.Is(err, nvmlsource.ErrInUse) {
			t.Fatalf("err = %v, want ErrInUse", err)
		}
	})
}
