// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

var (
	signalPowerDraw   = gpusignals.PowerDraw.ID()
	signalTemperature = gpusignals.TemperatureCelsius.ID()
)

type fakeNative struct {
	uuidErr           error
	inventoryErr      error
	enumerationErr    error
	gpuUUIDs          map[uint]string
	readStarted       chan struct{}
	uuidStarted       chan struct{}
	inventoryStarted  chan struct{}
	inventory         map[uint]*observation.GPUDevice
	releaseRead       chan struct{}
	gpuIndices        []uint
	enumerationCalls  atomic.Int32
	cleanupCalls      atomic.Int32
	initCalls         atomic.Int32
	inventoryCalls    atomic.Int32
	uuidCalls         atomic.Int32
	readCalls         atomic.Int32
	watchCleanupCalls atomic.Int32
	watchCalls        atomic.Int32
}

func (native *fakeNative) Init() (func(), error) {
	native.initCalls.Add(1)
	return func() { native.cleanupCalls.Add(1) }, nil
}

func (native *fakeNative) SupportedGPUIndices() ([]uint, error) {
	native.enumerationCalls.Add(1)
	if native.enumerationErr != nil {
		return nil, native.enumerationErr
	}
	if native.gpuIndices != nil {
		return native.gpuIndices, nil
	}
	return []uint{0}, nil
}

func (native *fakeNative) ReadGPUUUIDs(gpuIndices []uint) (map[uint]GPUIdentityEntry, error) {
	native.uuidCalls.Add(1)
	if native.uuidStarted != nil {
		close(native.uuidStarted)
		<-native.releaseRead
	}
	if native.uuidErr != nil {
		return nil, native.uuidErr
	}
	gpuUUIDs := make(map[uint]GPUIdentityEntry, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		gpuUUID := fmt.Sprintf("GPU-%d", gpuIndex)
		if native.gpuUUIDs != nil {
			gpuUUID = native.gpuUUIDs[gpuIndex]
		}
		gpuUUIDs[gpuIndex] = GPUIdentityEntry{Index: gpuIndex, UUID: gpuUUID}
	}
	return gpuUUIDs, nil
}

func (native *fakeNative) ReadGPUInventory(gpuIndices []uint) (map[uint]*observation.GPUInventoryEntry, error) {
	native.inventoryCalls.Add(1)
	if native.inventoryStarted != nil {
		close(native.inventoryStarted)
		<-native.releaseRead
	}
	if native.inventoryErr != nil {
		return nil, native.inventoryErr
	}
	gpus := make(map[uint]*observation.GPUInventoryEntry, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		gpu := &observation.GPUDevice{}
		if native.inventory != nil {
			gpu = native.inventory[gpuIndex]
		}
		if gpu != nil {
			gpus[gpuIndex] = &observation.GPUInventoryEntry{SourceIndex: uint32(gpuIndex), Gpu: gpu}
		}
	}
	return gpus, nil
}

func (native *fakeNative) WatchMetrics([]string) (func() error, error) {
	native.watchCalls.Add(1)
	return func() error {
		native.watchCleanupCalls.Add(1)
		return nil
	}, nil
}

func (native *fakeNative) ReadGPUMetrics(_ uint, signalIDs []string) (map[string]FieldValue, error) {
	native.readCalls.Add(1)
	if native.readStarted != nil {
		close(native.readStarted)
	}
	if native.releaseRead != nil {
		<-native.releaseRead
	}
	values := make(map[string]FieldValue, len(signalIDs))
	for _, signalID := range signalIDs {
		values[signalID] = FieldValue{Value: &observation.Value{
			Kind: &observation.Value_IntValue{IntValue: 1},
		}}
	}
	return values, nil
}

func TestMetricWatchName(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		expected string
	}{
		{name: "default", expected: "shared-signal-collection-123-4"},
		{name: "custom", prefix: "fleetint-daemon", expected: "fleetint-daemon-123-4"},
		{name: "trim whitespace", prefix: "  gpu-agent  ", expected: "gpu-agent-123-4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := metricWatchName(test.prefix, 123, 4); actual != test.expected {
				t.Fatalf("metricWatchName() = %q, want %q", actual, test.expected)
			}
		})
	}
}

func TestAddMetricsToWatchDeduplicatesSignals(t *testing.T) {
	source := newSource(&fakeNative{}, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{
		signalPowerDraw,
		signalPowerDraw,
	}); err != nil {
		t.Fatalf("AddMetricsToWatch() error = %v", err)
	}
	if err := source.AddMetricsToWatch([]string{
		signalTemperature,
	}); err != nil {
		t.Fatalf("AddMetricsToWatch() error = %v", err)
	}

	want := []string{
		signalPowerDraw,
		signalTemperature,
	}
	if !slices.Equal(source.metricWatchSignals, want) {
		t.Fatalf("watched signals = %v, want %v", source.metricWatchSignals, want)
	}
}

func TestInventoryUsesOneRead(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{0, 1}}
	source := newSource(native, &sync.Mutex{})
	cycle := newTestCycle(t, "inventory")
	identities, failure := source.GPUIdentities(context.Background(), cycle)
	if failure != nil {
		t.Fatal(failure)
	}
	snapshot := source.GPUInventory(context.Background(), cycle, identities)
	entries := snapshot.GetInventory().GetEntries()
	if len(entries) != 2 || entries[0].GetSourceIndex() != 0 || entries[0].GetGpu().GetUuid() != "GPU-0" || entries[1].GetSourceIndex() != 1 || entries[1].GetGpu().GetUuid() != "GPU-1" {
		t.Fatalf("Inventory() = %+v", snapshot)
	}
	if got := native.inventoryCalls.Load(); got != 1 {
		t.Fatalf("inventory read calls = %d, want 1", got)
	}
	if got := native.uuidCalls.Load(); got != 1 {
		t.Fatalf("UUID read calls = %d, want 1", got)
	}
	if err := observation.ValidateBatch(&observation.ObservationBatch{Inventory: &observation.HardwareInventory{
		GpuSnapshots: []*observation.GPUInventorySnapshot{snapshot},
	}}); err != nil {
		t.Fatalf("inventory snapshot is invalid: %v", err)
	}
}

func TestGPUIdentitiesReadsOnlyUUIDs(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{0, 1}, gpuUUIDs: map[uint]string{0: "GPU-0"}}
	entries, failure := newSource(native, &sync.Mutex{}).GPUIdentities(context.Background(), newTestCycle(t, "identities"))
	if failure != nil || len(entries) != 2 || entries[0].UUID != "GPU-0" || entries[1].UUIDError == nil {
		t.Fatalf("GPUIdentities() = %+v, %v", entries, failure)
	}
	if native.uuidCalls.Load() != 1 || native.inventoryCalls.Load() != 0 {
		t.Fatalf("UUID calls = %d, inventory calls = %d", native.uuidCalls.Load(), native.inventoryCalls.Load())
	}
}

func TestGPUIdentitiesUUIDFailurePreservesIndices(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{2, 7}, uuidErr: errors.New("DCGM UUID read failed")}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "uuid-error")
	entries, failure := source.GPUIdentities(context.Background(), cycle)
	if failure != nil || len(entries) != 2 {
		t.Fatalf("GPUIdentities() = %+v, %v", entries, failure)
	}
	for position, entry := range entries {
		if entry.Index != native.gpuIndices[position] || entry.UUID != "" || entry.UUIDError.GetOperation() != "identity" {
			t.Fatalf("identity = %+v", entry)
		}
		values := source.ReadGPUMetrics(context.Background(), cycle, entry.Index, []string{signalPowerDraw})
		if values[signalPowerDraw].Value == nil {
			t.Fatalf("metric read failed after UUID failure: %+v", values)
		}
	}
	snapshot := source.GPUInventory(context.Background(), cycle, entries)
	for _, entry := range snapshot.GetInventory().GetEntries() {
		if entry.GetFieldErrors()["uuid"] == nil || entry.GetCollectionError() != nil {
			t.Fatalf("UUID failure = %+v", entry)
		}
	}
	if native.enumerationCalls.Load() != 1 || native.uuidCalls.Load() != 1 || native.readCalls.Load() != 2 || native.inventoryCalls.Load() != 1 {
		t.Fatal("UUID failure changed membership or blocked subsequent collection")
	}
}

func TestGPUIdentitiesCancellationPreservesIndicesAndBlocksReentry(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{2, 7}, uuidStarted: make(chan struct{}), releaseRead: make(chan struct{})}
	t.Cleanup(func() { close(native.releaseRead) })
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "interrupted-uuid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan []GPUIdentityEntry, 1)
	failures := make(chan *observation.CollectionError, 1)
	go func() {
		identities, failure := source.GPUIdentities(ctx, cycle)
		result <- identities
		failures <- failure
	}()
	select {
	case <-native.uuidStarted:
	case <-time.After(time.Second):
		t.Fatal("UUID read did not start")
	}
	cancel()
	var entries []GPUIdentityEntry
	select {
	case entries = <-result:
	case <-time.After(time.Second):
		t.Fatal("identity read did not return after cancellation")
	}
	if failure := <-failures; failure != nil {
		t.Fatalf("UUID failure discarded membership: %v", failure)
	}
	if len(entries) != 2 || entries[0].Index != 2 || entries[1].Index != 7 {
		t.Fatalf("indices = %+v", entries)
	}
	for _, entry := range entries {
		if entry.UUIDError.GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Fatalf("UUID error = %v", entry.UUIDError)
		}
		for _, nextCycle := range []*collect.Cycle{cycle, newTestCycle(t, "next-cycle")} {
			values := source.ReadGPUMetrics(context.Background(), nextCycle, entry.Index, []string{signalPowerDraw})
			if values[signalPowerDraw].CollectionError == nil {
				t.Fatal("metric read entered DCGM while UUID read was running")
			}
		}
	}
	if native.readCalls.Load() != 0 {
		t.Fatal("native metric read started while UUID call was running")
	}
}

func TestGPUIdentitiesReenumeratesOnEachCycle(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{0}, gpuUUIDs: map[uint]string{0: "GPU-0"}}
	source := newSource(native, &sync.Mutex{})
	first, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "first-identities"))
	if failure != nil || len(first) != 1 || first[0].UUID != "GPU-0" {
		t.Fatalf("first GPUIdentities() = %+v, %v", first, failure)
	}
	native.gpuIndices = []uint{1}
	native.gpuUUIDs = map[uint]string{1: "GPU-1"}
	second, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "second-identities"))
	if failure != nil || len(second) != 1 || second[0].Index != 1 || second[0].UUID != "GPU-1" {
		t.Fatalf("second GPUIdentities() = %+v, %v", second, failure)
	}
	if native.uuidCalls.Load() != 2 || native.inventoryCalls.Load() != 0 {
		t.Fatalf("UUID calls = %d, inventory calls = %d", native.uuidCalls.Load(), native.inventoryCalls.Load())
	}
}

func TestInventoryPreservesMissingUUIDPerGPU(t *testing.T) {
	gpuUUID, model := "GPU-0", "H100"
	native := &fakeNative{
		gpuIndices: []uint{0, 1},
		inventory:  map[uint]*observation.GPUDevice{0: {Uuid: &gpuUUID}, 1: {Model: &model}},
	}
	source := newSource(native, &sync.Mutex{})
	cycle := newTestCycle(t, "missing-uuid")
	native.gpuUUIDs = map[uint]string{0: gpuUUID}
	identities, failure := source.GPUIdentities(context.Background(), cycle)
	if failure != nil {
		t.Fatal(failure)
	}
	snapshot := source.GPUInventory(context.Background(), cycle, identities)
	entries := snapshot.GetInventory().GetEntries()
	if len(entries) != 2 || entries[0].GetGpu().GetUuid() != gpuUUID || entries[1].GetGpu().GetModel() != model || entries[1].GetGpu().Uuid != nil || entries[1].GetFieldErrors()["uuid"] == nil {
		t.Fatalf("Inventory() = %+v", snapshot)
	}
}

func TestInventoryEmptyEnumerationSkipsQuery(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{}}
	source := newSource(native, &sync.Mutex{})
	cycle := newTestCycle(t, "empty-inventory")
	identities, failure := source.GPUIdentities(context.Background(), cycle)
	if failure != nil || len(identities) != 0 || native.uuidCalls.Load() != 0 {
		t.Fatalf("empty GPU identities = %+v, %v; UUID calls = %d", identities, failure, native.uuidCalls.Load())
	}
	snapshot := source.GPUInventory(context.Background(), cycle, identities)
	if snapshot.GetInventory() == nil || len(snapshot.GetInventory().GetEntries()) != 0 || native.inventoryCalls.Load() != 0 {
		t.Fatalf("Inventory() = %+v, inventory calls = %d", snapshot, native.inventoryCalls.Load())
	}
}

func TestInventoryReportsQueryFailure(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{2, 7}, inventoryErr: errors.New("DCGM read failed")}
	source := newSource(native, &sync.Mutex{})
	cycle := newTestCycle(t, "inventory-error")
	identities, failure := source.GPUIdentities(context.Background(), cycle)
	if failure != nil {
		t.Fatal(failure)
	}
	snapshot := source.GPUInventory(context.Background(), cycle, identities)
	entries := snapshot.GetInventory().GetEntries()
	if snapshot.GetCollectionError() != nil || len(entries) != 2 {
		t.Fatalf("enumerated membership lost: %+v", snapshot)
	}
	for index, gpuIndex := range native.gpuIndices {
		if entries[index].GetSourceIndex() != uint32(gpuIndex) || entries[index].GetCollectionError().GetOperation() != "inventory" {
			t.Fatalf("GPU %d result: %+v", gpuIndex, entries[index])
		}
	}
	if native.enumerationCalls.Load() != 1 || native.uuidCalls.Load() != 1 || entries[0].GetGpu().GetUuid() != "GPU-2" || entries[1].GetGpu().GetUuid() != "GPU-7" {
		t.Fatalf("inventory did not preserve original identities: %+v", entries)
	}
}

func TestGPUIdentitiesEnumerationFailure(t *testing.T) {
	native := &fakeNative{enumerationErr: errors.New("enumeration failed")}
	entries, failure := newSource(native, &sync.Mutex{}).GPUIdentities(context.Background(), newTestCycle(t, "enumeration-error"))
	if len(entries) != 0 || failure.GetOperation() != "identity" || native.inventoryCalls.Load() != 0 || native.uuidCalls.Load() != 0 {
		t.Fatalf("enumeration failure = %+v, %v", entries, failure)
	}
}

func TestInventoryCancellationPreservesMembershipAndBlocksReentry(t *testing.T) {
	native := &fakeNative{gpuIndices: []uint{2, 7}, inventoryStarted: make(chan struct{}), releaseRead: make(chan struct{})}
	t.Cleanup(func() { close(native.releaseRead) })
	source := newSource(native, &sync.Mutex{})
	cycle := newTestCycle(t, "interrupted-inventory")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan *observation.GPUInventorySnapshot, 1)
	identities, failure := source.GPUIdentities(context.Background(), cycle)
	if failure != nil {
		t.Fatal(failure)
	}
	go func() { result <- source.GPUInventory(ctx, cycle, identities) }()
	select {
	case <-native.inventoryStarted:
	case <-time.After(time.Second):
		t.Fatal("inventory read did not start")
	}
	cancel()
	select {
	case snapshot := <-result:
		entries := snapshot.GetInventory().GetEntries()
		if len(entries) != 2 || entries[0].GetSourceIndex() != 2 || entries[1].GetSourceIndex() != 7 || entries[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Fatalf("canceled inventory lost membership: %+v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("inventory did not return after cancellation")
	}
	for _, nextCycle := range []*collect.Cycle{cycle, newTestCycle(t, "next-cycle")} {
		entries, failure := source.GPUIdentities(context.Background(), nextCycle)
		if failure == nil || len(entries) != 0 || native.uuidCalls.Load() != 1 {
			t.Fatalf("entered DCGM while inventory still running: %+v", entries)
		}
	}
}

func TestReadGPUMetricsFreezesWatchAndCloseReleasesResources(t *testing.T) {
	native := &fakeNative{}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatalf("AddMetricsToWatch() error = %v", err)
	}

	values := source.ReadGPUMetrics(
		context.Background(),
		newTestCycle(t, "first-read"),
		0,
		[]string{signalPowerDraw},
	)
	if failure := values[signalPowerDraw].CollectionError; failure != nil {
		t.Fatalf("ReadGPUMetrics() collection error = %v", failure)
	}
	if err := source.AddMetricsToWatch([]string{signalTemperature}); err == nil {
		t.Fatal("AddMetricsToWatch() after ReadGPUMetrics() returned nil error")
	}

	if err := source.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if got := native.initCalls.Load(); got != 1 {
		t.Errorf("Init() calls = %d, want 1", got)
	}
	if got := native.watchCalls.Load(); got != 1 {
		t.Errorf("WatchMetrics() calls = %d, want 1", got)
	}
	if got := native.readCalls.Load(); got != 1 {
		t.Errorf("ReadGPUMetrics() calls = %d, want 1", got)
	}
	if got := native.watchCleanupCalls.Load(); got != 1 {
		t.Errorf("watch cleanup calls = %d, want 1", got)
	}
	if got := native.cleanupCalls.Load(); got != 1 {
		t.Errorf("client cleanup calls = %d, want 1", got)
	}
}

func TestReadGPUMetricsRejectsConcurrentNativeCall(t *testing.T) {
	native := &fakeNative{
		readStarted: make(chan struct{}),
		releaseRead: make(chan struct{}),
	}
	releaseNativeRead := func() {
		select {
		case <-native.releaseRead:
		default:
			close(native.releaseRead)
		}
	}
	t.Cleanup(releaseNativeRead)

	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatalf("AddMetricsToWatch() error = %v", err)
	}
	firstCycle := newTestCycle(t, "first-cycle")
	secondCycle := newTestCycle(t, "second-cycle")

	firstRead := make(chan map[string]FieldValue, 1)
	go func() {
		firstRead <- source.ReadGPUMetrics(
			context.Background(),
			firstCycle,
			0,
			[]string{signalPowerDraw},
		)
	}()

	select {
	case <-native.readStarted:
	case <-time.After(time.Second):
		t.Fatal("first native read did not start")
	}

	second := source.ReadGPUMetrics(
		context.Background(),
		secondCycle,
		0,
		[]string{signalPowerDraw},
	)
	failure := second[signalPowerDraw].CollectionError
	if failure == nil {
		t.Fatal("concurrent ReadGPUMetrics() returned no collection error")
	}
	if failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Errorf("category = %v, want %v", failure.GetCategory(), observation.CollectionErrorCategoryUnavailable)
	}
	if got := native.readCalls.Load(); got != 1 {
		t.Errorf("native ReadGPUMetrics() calls = %d, want 1", got)
	}

	releaseNativeRead()
	select {
	case values := <-firstRead:
		if failure := values[signalPowerDraw].CollectionError; failure != nil {
			t.Errorf("first ReadGPUMetrics() collection error = %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("first native read did not finish")
	}
}

func newTestCycle(t *testing.T, collectionID string) *collect.Cycle {
	t.Helper()
	current, err := collect.NewCycle(collectionID, time.Now().UTC())
	if err != nil {
		t.Fatalf("collect.NewCycle() error = %v", err)
	}
	return current
}
