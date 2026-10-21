// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// inventorySource returns a fixed batch and records each call's options.
type inventorySource struct {
	batch *observation.ObservationBatch
	err   error
	ops   []*collect.Op
}

func (*inventorySource) ID() string { return "gpu.test" }

func (*inventorySource) Signals() collect.Signals { return collect.Signals{gpusignals.PowerDraw} }

func (s *inventorySource) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	s.ops = append(s.ops, op)
	if s.err != nil {
		return nil, s.err
	}
	batch := proto.Clone(s.batch).(*observation.ObservationBatch)
	batch.CollectionId = op.Cycle().CollectionID()
	return batch, nil
}

func opened(source collect.Sensor) func() (collect.Sensor, error) {
	return func() (collect.Sensor, error) { return source, nil }
}

func testCycle(t *testing.T, id string) *collect.Cycle {
	t.Helper()
	cycle, err := collect.NewCycleWithTimeout(id, time.Now().Add(-time.Minute), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return cycle
}

func snapshotBatch(snapshots ...*observation.GPUInventorySnapshot) *observation.ObservationBatch {
	return &observation.ObservationBatch{Inventory: &observation.HardwareInventory{GpuSnapshots: snapshots}}
}

func inventory(entries ...*observation.GPUInventoryEntry) *observation.GPUInventorySnapshot {
	return &observation.GPUInventorySnapshot{
		Source:      "nvml",
		CollectedAt: timestamppb.New(time.Now().Add(-time.Second)),
		Outcome:     &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{Entries: entries}},
	}
}

func gpuEntry(index uint32, model string) *observation.GPUInventoryEntry {
	uuid := "GPU-" + string(rune('0'+index))
	return &observation.GPUInventoryEntry{SourceIndex: index, Gpu: &observation.GPUDevice{Uuid: &uuid, Model: &model}}
}

// failedEntry is a slot collect's NVML inventory reports with an entry-level
// error from operation.
func failedEntry(index uint32, operation string) *observation.GPUInventoryEntry {
	return &observation.GPUInventoryEntry{SourceIndex: index, Gpu: &observation.GPUDevice{}, CollectionError: &observation.CollectionError{
		Source: "nvml", Operation: operation, Category: observation.CollectionErrorCategoryUnavailable, SourceError: "ERROR_GPU_IS_LOST",
	}}
}

func enumerationFailure() *observation.GPUInventorySnapshot {
	return &observation.GPUInventorySnapshot{
		Source: "nvml", CollectedAt: timestamppb.New(time.Now().Add(-time.Second)),
		Outcome: &observation.GPUInventorySnapshot_CollectionError{CollectionError: &observation.CollectionError{
			Source: "nvml", Operation: "GetDevices", Category: observation.CollectionErrorCategoryUnavailable, Detail: "get device count: Driver Not Loaded",
		}},
	}
}

// measureCount reads GPUCount through the sensor and validates its batch.
func measureCount(t *testing.T, inv *Inventory) *observation.Observation {
	t.Helper()
	cycle := testCycle(t, "cycle")
	batch, err := inv.Sensor().Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(GPUCount.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if batch.GetCollectionId() != "cycle" || len(batch.GetObservations()) != 1 {
		t.Fatalf("batch=%v, want one observation in the active cycle", batch)
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatalf("invalid batch: %v", err)
	}
	return batch.GetObservations()[0]
}

func TestGPUCountFromInventory(t *testing.T) {
	const gb200 = "NVIDIA GB200"
	for _, tc := range []struct {
		snapshot *observation.GPUInventorySnapshot
		name     string
		want     int64
	}{
		{name: "healthy", snapshot: inventory(gpuEntry(0, gb200), gpuEntry(1, gb200), gpuEntry(2, gb200), gpuEntry(3, gb200)), want: 4},
		// A successful enumeration that found nothing proves zero GPUs.
		{name: "zero GPUs", snapshot: inventory(), want: 0},
		// A slot whose handle failed keeps its source index but is not
		// counted, as the legacy backend excludes unreadable handles.
		{name: "failed handle", snapshot: inventory(gpuEntry(0, gb200), failedEntry(1, handleOperation), gpuEntry(2, gb200), gpuEntry(3, gb200)), want: 3},
		{name: "every handle failed", snapshot: inventory(failedEntry(0, handleOperation), failedEntry(1, handleOperation)), want: 0},
		// A GPU whose handle loaded still counts when a later read failed,
		// as it does in the legacy backend.
		{name: "partial identity", snapshot: inventory(gpuEntry(0, gb200), failedEntry(1, "GetUUID"), failedEntry(2, "NewDevice"), failedEntry(3, "GetIndex")), want: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := measureCount(t, New(opened(&inventorySource{batch: snapshotBatch(tc.snapshot)})))
			if _, isInt := o.GetValue().GetKind().(*observation.Value_IntValue); !isInt || o.GetValue().GetIntValue() != tc.want {
				t.Fatalf("count=%v, want int %d", o.GetValue(), tc.want)
			}
			if o.GetTarget().GetNode() == nil || o.GetTarget().GetNode().GetId() != "" {
				t.Fatalf("target=%v, want NodeTarget(\"\")", o.GetTarget())
			}
			if o.GetSource() != "nvml" || !proto.Equal(o.GetObservedAt(), tc.snapshot.GetCollectedAt()) || o.GetUnit() != "1" {
				t.Fatalf("provenance not taken from the snapshot: %v", o)
			}
		})
	}
}

func TestGPUCountFailures(t *testing.T) {
	noTargets := observation.NewNoValueObservation(gpusignals.PowerDraw.ID(), gpusignals.PowerDraw.Unit(), "nvml", nil,
		timestamppb.New(time.Now().Add(-time.Second)), &observation.NoValue{Reason: observation.NoValueReasonNoTargets})
	for _, tc := range []struct {
		open     func() (collect.Sensor, error)
		name     string
		category observation.CollectionErrorCategory
	}{
		{name: "enumeration failure", open: opened(&inventorySource{batch: snapshotBatch(enumerationFailure())}), category: observation.CollectionErrorCategoryUnavailable},
		{name: "no snapshot", open: opened(&inventorySource{batch: &observation.ObservationBatch{}}), category: observation.CollectionErrorCategorySourceError},
		// NO_TARGETS is not inventory proof: without a snapshot the count is
		// unknown, not zero.
		{
			name:     "no targets without inventory",
			open:     opened(&inventorySource{batch: &observation.ObservationBatch{Observations: []*observation.Observation{noTargets}}}),
			category: observation.CollectionErrorCategorySourceError,
		},
		{name: "two snapshots", open: opened(&inventorySource{batch: snapshotBatch(inventory(), inventory())}), category: observation.CollectionErrorCategorySourceError},
		{name: "measure error", open: opened(&inventorySource{err: errors.New("source failed")}), category: observation.CollectionErrorCategorySourceError},
		{name: "open error", open: func() (collect.Sensor, error) { return nil, errors.New("no driver") }, category: observation.CollectionErrorCategoryUnavailable},
		{name: "nil source", open: func() (collect.Sensor, error) { return nil, nil }, category: observation.CollectionErrorCategoryUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := measureCount(t, New(tc.open))
			if o.GetCollectionError().GetCategory() != tc.category {
				t.Fatalf("observation=%v, want a %s collection error", o, tc.category)
			}
			if o.GetTarget() != nil {
				t.Fatalf("failure target=%v, want source-scoped", o.GetTarget())
			}
		})
	}
	t.Run("enumeration failure keeps detail", func(t *testing.T) {
		want := enumerationFailure()
		o := measureCount(t, New(opened(&inventorySource{batch: snapshotBatch(want)})))
		if !proto.Equal(o.GetCollectionError(), want.GetCollectionError()) || o.GetSource() != "nvml" {
			t.Fatalf("failure=%v source=%q, want %v from nvml", o.GetCollectionError(), o.GetSource(), want.GetCollectionError())
		}
	})
}

func TestOpenIsBoundedByCycleTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	inv := New(func() (collect.Sensor, error) {
		<-release
		return &inventorySource{}, nil
	})
	o := measureCount(t, inv)
	if o.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
		t.Fatalf("observation=%v, want a timeout", o)
	}
}

// Architecture discovery and the count read one snapshot per cycle, so the
// count needs no second enumeration.
func TestInventoryReadsOncePerCycle(t *testing.T) {
	source := &inventorySource{batch: snapshotBatch(inventory(gpuEntry(0, "NVIDIA GB200")))}
	opens := 0
	inv := New(func() (collect.Sensor, error) { opens++; return source, nil })
	cycle := testCycle(t, "first")
	first := inv.Snapshot(context.Background(), cycle)
	if _, err := inv.Sensor().Measure(context.Background(), collect.WithCycle(cycle)); err != nil {
		t.Fatal(err)
	}
	if again := inv.Snapshot(context.Background(), cycle); again != first || len(source.ops) != 1 {
		t.Fatalf("source measured %d times in one cycle, want 1", len(source.ops))
	}
	op := source.ops[0]
	selected, err := op.Signals(source.Signals())
	if err != nil {
		t.Fatal(err)
	}
	if !op.GPUInventory() || op.Cycle() != cycle || len(selected) != 0 {
		t.Fatalf("source op inventory=%v cycle=%v signals=%v, want inventory only in the active cycle", op.GPUInventory(), op.Cycle(), selected.IDs())
	}
	inv.Snapshot(context.Background(), testCycle(t, "second"))
	if len(source.ops) != 2 || opens != 1 {
		t.Fatalf("measures=%d opens=%d, want a fresh read per cycle from one opened source", len(source.ops), opens)
	}
}

func TestSensorContract(t *testing.T) {
	s := New(opened(&inventorySource{batch: snapshotBatch(inventory())})).Sensor()
	if s.ID() != SensorID || len(s.Signals()) != 1 || s.Signals()[0].ID() != GPUCount.ID() {
		t.Fatalf("id=%q signals=%v", s.ID(), s.Signals().IDs())
	}
	if _, err := s.Measure(context.Background()); err == nil {
		t.Fatal("measure without a cycle succeeded")
	}
	if _, err := s.Measure(context.Background(), collect.WithCycle(testCycle(t, "c")), collect.WithSignalID("gpu.unknown")); err == nil {
		t.Fatal("undeclared signal accepted")
	}
	batch, err := s.Measure(context.Background(), collect.WithCycle(testCycle(t, "c")), collect.WithSignalID())
	if err != nil || batch.GetCollectionId() != "c" || len(batch.GetObservations()) != 0 {
		t.Fatalf("empty selection: batch=%v err=%v", batch, err)
	}
}

func TestArchitecture(t *testing.T) {
	unreadModel := &observation.GPUInventoryEntry{SourceIndex: 0, Gpu: &observation.GPUDevice{}, FieldErrors: map[string]*observation.CollectionError{
		"model": {Source: "nvml", Operation: "GetName", Category: observation.CollectionErrorCategoryUnavailable},
	}}
	for _, tc := range []struct {
		snapshot *observation.GPUInventorySnapshot
		name     string
		want     string
		err      string
	}{
		{name: "recognized", snapshot: inventory(gpuEntry(0, "NVIDIA GB200")), want: "GB200"},
		{name: "first recognized wins", snapshot: inventory(gpuEntry(0, "NVIDIA A10"), gpuEntry(1, "NVIDIA GB300")), want: "GB300"},
		{name: "failed slot skipped", snapshot: inventory(failedEntry(0, handleOperation), gpuEntry(1, "NVIDIA H100 80GB HBM3")), want: "H100"},
		{name: "unrecognized", snapshot: inventory(gpuEntry(0, "NVIDIA A10")), want: ArchitectureUnknown},
		{name: "enumeration failure", snapshot: enumerationFailure(), err: "Driver Not Loaded"},
		{name: "no GPUs", snapshot: inventory(), err: "no GPUs enumerated"},
		{name: "no model read", snapshot: inventory(unreadModel, failedEntry(1, handleOperation)), err: "GetName"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Architecture(tc.snapshot)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("Architecture = %q, %v; want error containing %q", got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Architecture = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
