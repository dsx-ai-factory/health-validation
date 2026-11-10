// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

// fakeNVMLInstance is an nvmlsource.Instance over fixed enumeration records.
type fakeNVMLInstance struct {
	initErr error
	devices map[string]device.Device
	records []nvmlsource.InventoryDevice
}

func (*fakeNVMLInstance) NVMLExists() bool                    { return true }
func (i *fakeNVMLInstance) Devices() map[string]device.Device { return i.devices }
func (i *fakeNVMLInstance) Refresh() error                    { return i.initErr }
func (*fakeNVMLInstance) Shutdown() error                     { return nil }
func (i *fakeNVMLInstance) InitError() error                  { return i.initErr }

func (i *fakeNVMLInstance) InventoryDevices() []nvmlsource.InventoryDevice { return i.records }

func (i *fakeNVMLInstance) Read(read func(map[string]device.Device) error) error {
	return read(i.devices)
}

// fakeNVMLDevice answers only the basic reads discovery makes. Its nil
// embedded device panics on any other call, so enrichment cannot gate the run.
type fakeNVMLDevice struct {
	device.Device
	name  string
	index int
}

func (d *fakeNVMLDevice) UUID() string                     { return fmt.Sprintf("GPU-%d", d.index) }
func (d *fakeNVMLDevice) PCIBusID() string                 { return fmt.Sprintf("0000:%02x:00.0", d.index+1) }
func (d *fakeNVMLDevice) GetName() (string, gonvml.Return) { return d.name, gonvml.SUCCESS }

// stalledDevice is a GPU whose optional attribute reads hang, and whose name
// read hangs too when nameStalls is set.
type stalledDevice struct {
	*fakeNVMLDevice
	stall      chan struct{}
	nameStalls bool
}

func (d stalledDevice) GetName() (string, gonvml.Return) {
	if d.nameStalls {
		<-d.stall
	}
	return d.fakeNVMLDevice.GetName()
}

func (d stalledDevice) GetSerial() (string, gonvml.Return) {
	<-d.stall
	return "", gonvml.ERROR_NOT_SUPPORTED
}

func (d stalledDevice) GetVbiosVersion() (string, gonvml.Return) {
	<-d.stall
	return "", gonvml.ERROR_NOT_SUPPORTED
}

func (d stalledDevice) GetPciInfo() (gonvml.PciInfo, gonvml.Return) {
	<-d.stall
	return gonvml.PciInfo{}, gonvml.ERROR_NOT_SUPPORTED
}

// stalledInstance enumerates four GB200s whose enrichment reads hang.
func stalledInstance(stall chan struct{}, nameStalls bool) *fakeNVMLInstance {
	inst := &fakeNVMLInstance{devices: make(map[string]device.Device)}
	for i := range 4 {
		dev := stalledDevice{fakeNVMLDevice: &fakeNVMLDevice{index: i, name: "NVIDIA GB200"}, stall: stall, nameStalls: nameStalls}
		inst.devices[dev.UUID()] = dev
		inst.records = append(inst.records, nvmlsource.InventoryDevice{SourceIndex: uint32(i), Device: dev})
	}
	return inst
}

// inventoryHost is fakeHost with a real NVML basic inventory over inst.
type inventoryHost struct {
	inventory *gpuinventory.Inventory
	fakeHost
}

func (h inventoryHost) gpuInventory() *gpuinventory.Inventory { return h.inventory }

// runDiscovery runs the CLI over inst with a one-second collection timeout
// and reports the verdict and whether the CPU sensor ran.
func runDiscovery(t *testing.T, inst nvmlsource.Instance) (health.NodeVerdict, int, bool) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("collection_timeout_seconds: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cpuCalls := 0
	a := withCleanLinkFlap(New().WithProgramName("test"))
	withCleanXid(withCPU(a, cpuAppSensor{calls: &cpuCalls, possible: 1, online: 1, numa: 34}))
	a.host = inventoryHost{
		fakeHost:  fakeHost{b: spyBackend{called: new(bool)}},
		inventory: gpuinventory.New(func() (gpuinventory.Reader, error) { return gpuinventory.NVML(inst), nil }),
	}
	var stdout, stderr bytes.Buffer
	code := a.Run([]string{"run", "--node-id", testNodeID, "--config", config}, &stdout, &stderr)
	if code == exitError {
		t.Fatalf("Run exit = %d; stderr = %q", code, stderr.String())
	}
	var v health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal verdict: %v; stdout = %s", err, stdout.String())
	}
	return v, code, cpuCalls > 0
}

func checkResult(v health.NodeVerdict, id string) (health.CheckVerdict, bool) {
	for _, r := range v.CheckResults {
		if r.CheckID == id {
			return r, true
		}
	}
	return health.CheckVerdict{}, false
}

// Optional GPU attributes are not a prerequisite for the run: with every
// enrichment read hung, discovery still names the architecture from the GPU
// model, and gpu_count and the independent CPU checks are evaluated.
func TestRunDoesNotWaitForGPUEnrichment(t *testing.T) {
	stall := make(chan struct{})
	defer close(stall)
	v, _, cpuCollected := runDiscovery(t, stalledInstance(stall, false))
	if r, found := checkResult(v, "architecture_discovery"); found {
		t.Fatalf("discovery failed on an enrichment read: %v", r.Messages)
	}
	if r, found := checkResult(v, "gpu_count"); !found || r.Severity != health.Pass {
		t.Errorf("gpu_count = %+v (found=%v), want PASS for four GPUs", r, found)
	}
	if _, found := checkResult(v, "cpu_count"); !found || !cpuCollected {
		t.Errorf("cpu_count evaluated=%v, CPU collected=%v; want independent checks to run", found, cpuCollected)
	}
}

// Basic inventory is the prerequisite: when no GPU model can be read or
// enumeration fails, the run reports a discovery failure, bounded by the
// collection timeout, and collects nothing else.
func TestRunFailsDiscoveryWithoutBasicInventory(t *testing.T) {
	stall := make(chan struct{})
	defer close(stall)
	for _, tc := range []struct {
		inst nvmlsource.Instance
		name string
	}{
		{name: "model read hangs", inst: stalledInstance(stall, true)},
		{name: "enumeration failed", inst: &fakeNVMLInstance{initErr: fmt.Errorf("get device count: %s", gonvml.ErrorString(gonvml.ERROR_UNKNOWN))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, code, cpuCollected := runDiscovery(t, tc.inst)
			if r, found := checkResult(v, "architecture_discovery"); !found || r.Severity != health.Critical || len(v.CheckResults) != 1 {
				t.Fatalf("results = %+v, want only a CRITICAL architecture_discovery", v.CheckResults)
			}
			if code != exitUnhealthy || cpuCollected {
				t.Errorf("exit = %d, CPU collected = %v; want exitUnhealthy before any collection", code, cpuCollected)
			}
		})
	}
}
