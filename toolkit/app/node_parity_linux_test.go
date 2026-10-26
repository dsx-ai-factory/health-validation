// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// Cases expected to finish get a generous timeout so slow or race-instrumented
// runs cannot time them out; a blocked case waits only the short one.
const (
	completeTimeout = 30 * time.Second
	blockedTimeout  = 50 * time.Millisecond
)

// gpuSlot is one NVML device index as both paths see it.
type gpuSlot struct {
	name string
	// handle fails DeviceGetHandleByIndex when not SUCCESS.
	handle gonvml.Return
	// uuid fails GetUUID when not SUCCESS; the handle still loads.
	uuid gonvml.Return
}

var (
	gb200     = gpuSlot{name: "NVIDIA GB200"}
	lostGPU   = gpuSlot{handle: gonvml.ERROR_GPU_IS_LOST}
	noUUIDGPU = gpuSlot{name: "NVIDIA GB200", uuid: gonvml.ERROR_UNKNOWN}
)

// parityNode describes the hardware both paths read.
type parityNode struct {
	block chan struct{}
	slots []gpuSlot
	// count fails DeviceGetCount (enumeration) when not SUCCESS.
	count gonvml.Return
}

// timeout is the collection timeout for a run over n.
func (n parityNode) timeout() time.Duration {
	if n.block != nil {
		return blockedTimeout
	}
	return completeTimeout
}

// legacyLibrary is the go-nvml mock the legacy NVML backend reads.
func (n parityNode) legacyLibrary() gonvml.Interface {
	return &mock.Interface{
		InitFunc:     func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc: func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) {
			if n.block != nil {
				<-n.block
			}
			return len(n.slots), n.count
		},
		DeviceGetHandleByIndexFunc: func(i int) (gonvml.Device, gonvml.Return) {
			slot := n.slots[i]
			if slot.handle != gonvml.SUCCESS {
				return nil, slot.handle
			}
			return &mock.Device{
				GetNameFunc: func() (string, gonvml.Return) { return slot.name, gonvml.SUCCESS },
				GetUUIDFunc: func() (string, gonvml.Return) { return fmt.Sprintf("GPU-%d", i), slot.uuid },
			}, gonvml.SUCCESS
		},
	}
}

// collectInstance is the collect NVML source over the same slots, in the
// records collect's enumeration produces: a failed handle keeps its index
// without a device, and a failed UUID keeps the device.
func (n parityNode) collectInstance() *parityInstance {
	inst := &parityInstance{block: n.block}
	if n.count != gonvml.SUCCESS {
		inst.initErr = fmt.Errorf("get device count: %s", gonvml.ErrorString(n.count))
		return inst
	}
	inst.devices = make(map[string]device.Device)
	for i, slot := range n.slots {
		record := nvmlsource.InventoryDevice{SourceIndex: uint32(i), HandleReturn: slot.handle, UUIDReturn: slot.uuid}
		if slot.handle != gonvml.SUCCESS {
			record.Error = fmt.Errorf("device %d handle: %s", i, gonvml.ErrorString(slot.handle))
			inst.records = append(inst.records, record)
			continue
		}
		dev := &parityDevice{index: i, slot: slot}
		record.Device = dev
		if slot.uuid != gonvml.SUCCESS {
			record.Error = fmt.Errorf("device %d UUID: %s", i, gonvml.ErrorString(slot.uuid))
		} else {
			inst.devices[dev.UUID()] = dev
		}
		inst.records = append(inst.records, record)
	}
	return inst
}

// parityInstance is an nvmlsource.Instance over fixed enumeration records.
type parityInstance struct {
	initErr error
	block   chan struct{}
	devices map[string]device.Device
	records []nvmlsource.InventoryDevice
	reads   atomic.Int32
}

func (*parityInstance) NVMLExists() bool                    { return true }
func (i *parityInstance) Devices() map[string]device.Device { return i.devices }
func (i *parityInstance) Refresh() error                    { return i.initErr }
func (*parityInstance) Shutdown() error                     { return nil }
func (i *parityInstance) InitError() error                  { return i.initErr }

func (i *parityInstance) InventoryDevices() []nvmlsource.InventoryDevice { return i.records }

func (i *parityInstance) Read(read func(map[string]device.Device) error) error {
	i.reads.Add(1)
	if i.block != nil {
		<-i.block
	}
	return read(i.devices)
}

// parityDevice answers only the basic reads discovery makes. Its nil embedded
// device panics on any other call, so enrichment cannot gate the run.
type parityDevice struct {
	device.Device
	slot  gpuSlot
	index int
}

func (d *parityDevice) UUID() string                     { return fmt.Sprintf("GPU-%d", d.index) }
func (d *parityDevice) GetName() (string, gonvml.Return) { return d.slot.name, gonvml.SUCCESS }

// parityHost serves one collect NVML inventory to the app.
type parityHost struct {
	inventory *gpuinventory.Inventory
}

func (parityHost) gpuBackend() toolkit.GPUBackend { panic("legacy GPU backend is not under test") }
func (parityHost) nvlinkBackend() toolkit.NVLinkBackend {
	panic("legacy NVLink backend is not under test")
}
func (h parityHost) gpuInventory() *gpuinventory.Inventory { return h.inventory }
func (parityHost) close() error                            { return nil }

func parityCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	if err := cat.Register(node.GPUCount()); err != nil {
		t.Fatal(err)
	}
	return cat
}

// gpuCountVerdict is what must not change at the node cutover.
type gpuCountVerdict struct {
	observed any
	action   health.RecommendedAction
	severity health.Severity
	overall  health.Severity
}

func gpuCountResult(t *testing.T, v health.NodeVerdict) gpuCountVerdict {
	t.Helper()
	if len(v.CheckResults) != 1 || v.CheckResults[0].CheckID != "gpu_count" {
		t.Fatalf("want one evaluated gpu_count result, got %+v (skipped %+v)", v.CheckResults, v.SkippedChecks)
	}
	r := v.CheckResults[0]
	return gpuCountVerdict{observed: r.ObservedValues[node.SignalGPUCount], action: r.RecommendedAction, severity: r.Severity, overall: v.OverallSeverity}
}

// discovery is an architecture discovery outcome; errors compare by presence.
type discovery struct {
	arch   string
	failed bool
}

// runCollect runs the stock CLI's collect node path on one cycle: architecture
// discovery, then the built-in node source and adapter.
func runCollect(t *testing.T, n parityNode) (discovery, health.NodeVerdict, int32) {
	t.Helper()
	inst := n.collectInstance()
	a := New().WithCPUBackend(toolkit.MockCPU(cpu.CPUMeasurement{}))
	a.host = parityHost{inventory: gpuinventory.New(func() (gpuinventory.Reader, error) { return gpuinventory.NVML(inst), nil })}
	cycle, err := newCycle(n.timeout())
	if err != nil {
		t.Fatal(err)
	}
	arch, err := gpuinventory.Architecture(a.host.gpuInventory().Snapshot(context.Background(), cycle))
	sources, adapters := a.builtinCollect()
	sensors, err2 := a.collectSensorsIn(cycle, sources, adapters, nil)
	if err2 != nil {
		t.Fatal(err2)
	}
	verdict, err2 := toolkit.CollectWithOptions(parityCatalog(t), toolkit.CollectionOptions{Timeout: n.timeout()}, sensors...)
	if err2 != nil {
		t.Fatal(err2)
	}
	return discovery{arch: arch, failed: err != nil}, verdict, inst.reads.Load()
}

func runLegacy(t *testing.T, n parityNode) (discovery, health.NodeVerdict) {
	t.Helper()
	verdict, err := toolkit.CollectWithOptions(parityCatalog(t), toolkit.CollectionOptions{Timeout: n.timeout()},
		toolkit.NewNodeSensor(nvml.NewWithLibrary(n.legacyLibrary())))
	if err != nil {
		t.Fatal(err)
	}
	if n.block != nil {
		// Legacy discovery is unbounded; a stalled driver never reaches it.
		return discovery{failed: true}, verdict
	}
	arch, err := nvml.NewWithLibrary(n.legacyLibrary()).Architecture()
	return discovery{arch: arch, failed: err != nil}, verdict
}

// TestNodeCollectParity is the node cutover gate: for the same hardware, the
// collect path (one NVML inventory read shared by architecture discovery and
// the node adapter) must give the legacy NVML backend's architecture and
// gpu_count verdict, including its recommended action.
func TestNodeCollectParity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		node     parityNode
		severity health.Severity
	}{
		{name: "healthy", node: parityNode{slots: []gpuSlot{gb200, gb200, gb200, gb200}}, severity: health.Pass},
		{name: "zero GPUs", node: parityNode{}, severity: health.Critical},
		{name: "missing GPU", node: parityNode{slots: []gpuSlot{gb200, gb200, gb200}}, severity: health.Critical},
		{name: "failed slot", node: parityNode{slots: []gpuSlot{gb200, lostGPU, gb200, gb200}}, severity: health.Critical},
		// A GPU whose handle loads counts even when its UUID does not read.
		{name: "partial identity", node: parityNode{slots: []gpuSlot{gb200, noUUIDGPU, gb200, gb200}}, severity: health.Pass},
		{name: "every slot failed", node: parityNode{slots: []gpuSlot{lostGPU, lostGPU, lostGPU, lostGPU}}, severity: health.Critical},
		{name: "enumeration failure", node: parityNode{count: gonvml.ERROR_UNKNOWN}, severity: health.Critical},
		{name: "collection timeout", node: parityNode{slots: []gpuSlot{gb200, gb200, gb200, gb200}, block: make(chan struct{})}, severity: health.Critical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.node.block != nil {
				defer close(tc.node.block)
			}
			legacyArch, legacy := runLegacy(t, tc.node)
			collectArch, bridged, reads := runCollect(t, tc.node)

			want, got := gpuCountResult(t, legacy), gpuCountResult(t, bridged)
			if want.severity != tc.severity {
				t.Fatalf("legacy gpu_count severity=%v, want %v; the case no longer covers what it names", want.severity, tc.severity)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("gpu_count verdict differs:\n collect=%+v\n  legacy=%+v", got, want)
			}
			if collectArch != legacyArch {
				t.Errorf("architecture differs: collect=%+v legacy=%+v", collectArch, legacyArch)
			}
			if reads > 1 {
				t.Errorf("collect read the NVML inventory %d times in one cycle, want at most 1", reads)
			}
		})
	}
}

// TestNodeCollectTimeoutRecommendsRetry pins the timeout case's action, so the
// parity above cannot pass with both paths draining for the wrong reason.
func TestNodeCollectTimeoutRecommendsRetry(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	discovered, verdict, _ := runCollect(t, parityNode{slots: []gpuSlot{gb200}, block: block})
	got := gpuCountResult(t, verdict)
	if !discovered.failed || got.severity != health.Critical || !got.action.Equal(health.Drain(health.RemediationActionRetryCollection)) {
		t.Fatalf("discovery=%+v gpu_count=%+v, want a failed discovery and CRITICAL with retry collection", discovered, got)
	}
}
