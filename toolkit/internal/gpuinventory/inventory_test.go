// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeReader returns a fixed snapshot and counts reads.
type fakeReader struct {
	snapshot *observation.GPUInventorySnapshot
	err      error
	block    chan struct{}
	reads    int
}

func (*fakeReader) ID() string { return "fake" }

func (r *fakeReader) Read(context.Context) (*observation.GPUInventorySnapshot, error) {
	r.reads++
	if r.block != nil {
		<-r.block
	}
	return r.snapshot, r.err
}

func opened(reader Reader) func() (Reader, error) {
	return func() (Reader, error) { return reader, nil }
}

func testCycle(t *testing.T, id string) *collect.Cycle {
	t.Helper()
	cycle, err := collect.NewCycleWithTimeout(id, time.Now().Add(-time.Minute), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return cycle
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
			o := measureCount(t, New(opened(&fakeReader{snapshot: tc.snapshot})))
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
	for _, tc := range []struct {
		open     func() (Reader, error)
		name     string
		category observation.CollectionErrorCategory
	}{
		{name: "enumeration failure", open: opened(&fakeReader{snapshot: enumerationFailure()}), category: observation.CollectionErrorCategoryUnavailable},
		{name: "read error", open: opened(&fakeReader{err: errors.New("NVML in use")}), category: observation.CollectionErrorCategoryUnavailable},
		{name: "no snapshot", open: opened(&fakeReader{}), category: observation.CollectionErrorCategoryUnavailable},
		{name: "open error", open: func() (Reader, error) { return nil, errors.New("no driver") }, category: observation.CollectionErrorCategoryUnavailable},
		{name: "nil reader", open: func() (Reader, error) { return nil, nil }, category: observation.CollectionErrorCategoryUnavailable},
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
		o := measureCount(t, New(opened(&fakeReader{snapshot: want})))
		if !proto.Equal(o.GetCollectionError(), want.GetCollectionError()) || o.GetSource() != "nvml" {
			t.Fatalf("failure=%v source=%q, want %v from nvml", o.GetCollectionError(), o.GetSource(), want.GetCollectionError())
		}
	})
}

func TestOpenAndReadAreBoundedByCycleTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	for name, inv := range map[string]*Inventory{
		"open": New(func() (Reader, error) {
			<-release
			return &fakeReader{}, nil
		}),
		"read": New(opened(&fakeReader{snapshot: inventory(), block: release})),
	} {
		t.Run(name, func(t *testing.T) {
			if o := measureCount(t, inv); o.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout {
				t.Fatalf("observation=%v, want a timeout", o)
			}
		})
	}
}

func TestCauseKeepsTheFailureError(t *testing.T) {
	busy := fmt.Errorf("%w: test", ErrSourceBusy)
	for name, tc := range map[string]struct {
		inv  *Inventory
		busy bool
	}{
		"open":    {inv: New(func() (Reader, error) { return nil, busy }), busy: true},
		"read":    {inv: New(opened(&fakeReader{err: busy})), busy: true},
		"success": {inv: New(opened(&fakeReader{snapshot: inventory(gpuEntry(0, "NVIDIA GB200"))}))},
	} {
		t.Run(name, func(t *testing.T) {
			cycle := testCycle(t, "cycle")
			snapshot := tc.inv.Snapshot(context.Background(), cycle)
			if got := errors.Is(tc.inv.Cause(cycle), ErrSourceBusy); got != tc.busy {
				t.Fatalf("Cause = %v, busy = %v, want %v", tc.inv.Cause(cycle), got, tc.busy)
			}
			if tc.busy && snapshot.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
				t.Fatalf("snapshot = %v, want an UNAVAILABLE collection error", snapshot)
			}
			if err := tc.inv.Cause(testCycle(t, "other")); err != nil {
				t.Fatalf("Cause for another cycle = %v, want nil", err)
			}
		})
	}
}

// Architecture discovery and the count read one snapshot per cycle, so the
// count needs no second enumeration.
func TestInventoryReadsOncePerCycle(t *testing.T) {
	reader := &fakeReader{snapshot: inventory(gpuEntry(0, "NVIDIA GB200"))}
	opens := 0
	inv := New(func() (Reader, error) { opens++; return reader, nil })
	cycle := testCycle(t, "first")
	first := inv.Snapshot(context.Background(), cycle)
	if _, err := inv.Sensor().Measure(context.Background(), collect.WithCycle(cycle)); err != nil {
		t.Fatal(err)
	}
	if again := inv.Snapshot(context.Background(), cycle); again != first || reader.reads != 1 {
		t.Fatalf("read %d times in one cycle, want 1", reader.reads)
	}
	inv.Snapshot(context.Background(), testCycle(t, "second"))
	if reader.reads != 2 || opens != 1 {
		t.Fatalf("reads=%d opens=%d, want a fresh read per cycle from one opened reader", reader.reads, opens)
	}
}

func TestSensorContract(t *testing.T) {
	s := New(opened(&fakeReader{snapshot: inventory()})).Sensor()
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
		{name: "B200 after GB200", snapshot: inventory(gpuEntry(0, "NVIDIA B200")), want: "B200"},
		{name: "first recognized wins", snapshot: inventory(gpuEntry(0, "NVIDIA A10"), gpuEntry(1, "NVIDIA GB300")), want: "GB300"},
		{name: "failed slot skipped", snapshot: inventory(failedEntry(0, handleOperation), gpuEntry(1, "NVIDIA H100 80GB HBM3")), want: "H100"},
		{name: "VR200 by name", snapshot: inventory(gpuEntry(0, "NVIDIA VR200")), want: "VR200"},
		{name: "unrecognized", snapshot: inventory(gpuEntry(0, "NVIDIA A10")), want: ArchitectureUnknown},
		{name: "generic name", snapshot: inventory(gpuEntry(0, "NVIDIA Graphics Device")), want: ArchitectureUnknown},
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

func TestCachedNeverReads(t *testing.T) {
	reader := &fakeReader{snapshot: inventory(gpuEntry(0, "NVIDIA GB200"))}
	opens := 0
	inv := New(func() (Reader, error) { opens++; return reader, nil })
	cycle := testCycle(t, "first")
	if got := inv.Cached(cycle); got != nil || opens != 0 || reader.reads != 0 {
		t.Fatalf("Cached before a read = %v after %d opens and %d reads, want nil without reading", got, opens, reader.reads)
	}
	read := inv.Snapshot(context.Background(), cycle)
	if got := inv.Cached(cycle); got != read || reader.reads != 1 {
		t.Fatalf("Cached = %v after %d reads, want the cycle's snapshot from one read", got, reader.reads)
	}
	if got := inv.Cached(testCycle(t, "second")); got != nil {
		t.Fatalf("Cached for another cycle = %v, want nil", got)
	}
}

func TestUUIDsByPCI(t *testing.T) {
	gpu := func(uuid, busID string) *observation.GPUInventoryEntry {
		device := &observation.GPUDevice{}
		if uuid != "" {
			device.Uuid = &uuid
		}
		if busID != "" {
			device.BusId = &busID
		}
		return &observation.GPUInventoryEntry{Gpu: device}
	}
	snapshot := &observation.GPUInventorySnapshot{Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{Entries: []*observation.GPUInventoryEntry{
		gpu("GPU-a", "0000:89:00.0"),
		gpu("GPU-b", "00000008:06:00.0"),
		gpu("GPU-c", ""),
		gpu("", "0000:c1:00.0"),
	}}}}
	want := map[string]string{"0000:89:00": "GPU-a", "0008:06:00": "GPU-b"}
	if got := UUIDsByPCI(snapshot); !maps.Equal(got, want) {
		t.Fatalf("UUIDsByPCI = %v, want %v", got, want)
	}
	if got := UUIDsByPCI(nil); len(got) != 0 {
		t.Fatalf("UUIDsByPCI(nil) = %v, want empty", got)
	}
}
