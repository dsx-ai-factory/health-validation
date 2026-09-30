// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

type fakeInventorySource struct {
	snapshot        *observation.GPUInventorySnapshot
	identityFailure *observation.CollectionError
	identities      []dcgmsource.GPUIdentityEntry
	inventoryReads  int
	identityReads   int
	metricReads     int
}

func (*fakeInventorySource) ID() string { return dcgmsource.Name }

func (source *fakeInventorySource) GPUInventory(context.Context, *collect.Cycle, []dcgmsource.GPUIdentityEntry) *observation.GPUInventorySnapshot {
	source.inventoryReads++
	return source.snapshot
}

func (source *fakeInventorySource) GPUIdentities(context.Context, *collect.Cycle) ([]dcgmsource.GPUIdentityEntry, *observation.CollectionError) {
	source.identityReads++
	return source.identities, source.identityFailure
}

func (source *fakeInventorySource) ReadGPUMetrics(_ context.Context, _ *collect.Cycle, _ uint, signalIDs []string) map[string]dcgmsource.FieldValue {
	source.metricReads++
	values := make(map[string]dcgmsource.FieldValue)
	for _, signalID := range signalIDs {
		values[signalID] = dcgmsource.FieldValue{Value: &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: 100}}}
	}
	return values
}

func TestMeasureInventoryOnly(t *testing.T) {
	cycle, err := collect.NewCycle("inventory-only", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &observation.GPUInventorySnapshot{
		Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{}},
	}
	source := &fakeInventorySource{snapshot: snapshot, identities: []dcgmsource.GPUIdentityEntry{{Index: 1, UUID: "GPU-1"}}}
	sensor := &sensor{source: source, signals: supportedSignals}

	batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(), collect.WithGPUInventory())
	if err != nil {
		t.Fatalf("Measure() error = %v", err)
	}
	if batch.GetCollectionId() != "inventory-only" {
		t.Fatalf("collection ID = %q, want inventory-only", batch.GetCollectionId())
	}
	if got := batch.GetInventory().GetGpuSnapshots(); len(got) != 1 || got[0] != snapshot {
		t.Fatalf("inventory snapshots = %v, want source snapshot", got)
	}
	if len(batch.GetObservations()) != 0 || source.inventoryReads != 1 || source.identityReads != 1 || source.metricReads != 0 {
		t.Fatalf("inventory-only collection: observations=%d inventory reads=%d identity reads=%d metric reads=%d", len(batch.GetObservations()), source.inventoryReads, source.identityReads, source.metricReads)
	}
}

func TestMeasureNoSignalsWithoutInventoryRequest(t *testing.T) {
	cycle, err := collect.NewCycle("no-selection", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeInventorySource{}
	sensor := &sensor{source: source, signals: supportedSignals}

	batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID())
	if err != nil {
		t.Fatalf("Measure() error = %v", err)
	}
	if batch.GetInventory() != nil || len(batch.GetObservations()) != 0 || source.inventoryReads != 0 || source.identityReads != 0 {
		t.Fatalf("unrequested collection produced inventory or observations: %+v", batch)
	}
}

func TestMeasureMetricsDoNotReturnInventory(t *testing.T) {
	cycle, err := collect.NewCycle("metrics-only", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeInventorySource{identities: []dcgmsource.GPUIdentityEntry{{Index: 1, UUID: "GPU-1"}}}
	sensor := &sensor{source: source, signals: supportedSignals}

	batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()))
	if err != nil {
		t.Fatalf("Measure() error = %v", err)
	}
	if batch.GetInventory() != nil {
		t.Fatalf("metrics-only collection returned inventory: %v", batch.GetInventory())
	}
	if len(batch.GetObservations()) != 1 || source.inventoryReads != 0 || source.identityReads != 1 || source.metricReads != 1 {
		t.Fatalf("metrics-only collection: observations=%d inventory reads=%d identity reads=%d metric reads=%d", len(batch.GetObservations()), source.inventoryReads, source.identityReads, source.metricReads)
	}
}

func TestMeasureMetricsAndInventoryUseOneInventoryRead(t *testing.T) {
	cycle, err := collect.NewCycle("both", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	gpuUUID := "GPU-1"
	source := &fakeInventorySource{identities: []dcgmsource.GPUIdentityEntry{{Index: 1, UUID: gpuUUID}}, snapshot: &observation.GPUInventorySnapshot{
		Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{
			Entries: []*observation.GPUInventoryEntry{{
				SourceIndex: 1,
				Gpu:         &observation.GPUDevice{Uuid: &gpuUUID},
			}},
		}},
	}}
	sensor := &sensor{source: source, signals: supportedSignals}
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()), collect.WithGPUInventory())
	if err != nil {
		t.Fatalf("Measure() error = %v", err)
	}
	if batch.GetInventory() == nil || len(batch.GetObservations()) != 1 || source.inventoryReads != 1 || source.identityReads != 1 || source.metricReads != 1 {
		t.Fatalf("combined collection: observations=%d inventory reads=%d identity reads=%d metric reads=%d", len(batch.GetObservations()), source.inventoryReads, source.identityReads, source.metricReads)
	}
}

func TestResolveSignalsToWatch(t *testing.T) {
	t.Run("defaults to every signal", func(t *testing.T) {
		resolved, err := resolveSignalsToWatch(nil)
		if err != nil {
			t.Fatalf("resolveSignalsToWatch() error = %v", err)
		}
		if !slices.Equal(resolved, supportedSignals) {
			t.Fatalf("resolved signals = %v, want %v", resolved, supportedSignals)
		}
	})

	t.Run("removes duplicates in request order", func(t *testing.T) {
		requested := []string{
			gpusignals.PowerDraw.ID(),
			gpusignals.TemperatureCelsius.ID(),
			gpusignals.PowerDraw.ID(),
		}
		resolved, err := resolveSignalsToWatch(requested)
		if err != nil {
			t.Fatalf("resolveSignalsToWatch() error = %v", err)
		}
		want := collect.Signals{
			gpusignals.PowerDraw,
			gpusignals.TemperatureCelsius,
		}
		if !slices.Equal(resolved, want) {
			t.Fatalf("resolved signals = %v, want %v", resolved, want)
		}
	})

	t.Run("rejects unsupported signal", func(t *testing.T) {
		if _, err := resolveSignalsToWatch([]string{"gpu.unknown"}); err == nil {
			t.Fatal("resolveSignalsToWatch() returned nil error")
		}
	})
}

func TestSensorID(t *testing.T) {
	if got := (&sensor{}).ID(); got != "gpu.dcgm" {
		t.Fatalf("Sensor.ID() = %q, want %q", got, "gpu.dcgm")
	}
}

func TestMeasureEnumerationFailureStopsCollection(t *testing.T) {
	for _, metrics := range []bool{false, true} {
		source := &fakeInventorySource{identityFailure: &observation.CollectionError{Operation: "identity", Category: observation.CollectionErrorCategoryUnavailable}}
		sensor := &sensor{source: source, signals: supportedSignals}
		cycle, err := collect.NewCycle("identity-failure", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		selected := []string{}
		if metrics {
			selected = append(selected, gpusignals.PowerDraw.ID())
		}
		batch, err := sensor.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(selected...), collect.WithGPUInventory())
		if err != nil {
			t.Fatal(err)
		}
		if source.identityReads != 1 || source.inventoryReads != 0 || source.metricReads != 0 {
			t.Fatal("attempted collection after identity failure")
		}
		if batch.GetInventory().GetGpuSnapshots()[0].GetCollectionError() == nil {
			t.Fatal("identity failure missing from inventory snapshot")
		}
		if len(batch.GetObservations()) != len(selected) {
			t.Fatalf("observations = %v", batch.GetObservations())
		}
		if metrics && (batch.Observations[0].GetTarget() != nil || batch.Observations[0].GetCollectionError() == nil) {
			t.Fatal("identity failure must be source-wide")
		}
	}
}

func TestMeasurePartialInventoryDoesNotBlockMetrics(t *testing.T) {
	gpuUUID, model := "GPU-1", "H100"
	failure := &observation.CollectionError{Source: "dcgm", Operation: "inventory", Category: observation.CollectionErrorCategoryUnavailable}
	for _, wholeQueryFailed := range []bool{false, true} {
		entry := &observation.GPUInventoryEntry{SourceIndex: 1, Gpu: &observation.GPUDevice{Uuid: &gpuUUID}}
		if wholeQueryFailed {
			entry.CollectionError = failure
		} else {
			entry.Gpu.Model = &model
			entry.FieldErrors = map[string]*observation.CollectionError{"serial": failure}
		}
		source := &fakeInventorySource{
			identities: []dcgmsource.GPUIdentityEntry{{Index: 1, UUID: gpuUUID}, {Index: 2, UUIDError: failure}},
			snapshot:   &observation.GPUInventorySnapshot{Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{Entries: []*observation.GPUInventoryEntry{entry}}}},
		}
		cycle, err := collect.NewCycle("partial-inventory", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		batch, err := (&sensor{source: source, signals: supportedSignals}).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()), collect.WithGPUInventory())
		if err != nil {
			t.Fatal(err)
		}
		if source.identityReads != 1 || source.inventoryReads != 1 || source.metricReads != 2 {
			t.Fatalf("reads: %+v", source)
		}
		if len(batch.Observations) != 2 || batch.Observations[0].GetValue() == nil || batch.Observations[0].GetTarget().GetGpu().GetUuid() != gpuUUID || batch.Observations[1].GetValue() == nil || batch.Observations[1].GetCollectionError() != nil {
			t.Fatalf("metric outcomes: %+v", batch.Observations)
		}
		if target := batch.Observations[1].GetTarget().GetGpu(); target.GetUuid() != "" || target.GetSourceLocalId() != "dcgm-index-2" {
			t.Fatalf("metric target without UUID = %v", target)
		}
		if validationErr := observation.ValidateBatch(batch); validationErr != nil {
			t.Fatal(validationErr)
		}
		if batch.GetInventory().GetGpuSnapshots()[0] != source.snapshot {
			t.Fatal("inventory errors were replaced")
		}
	}
}

func TestDeviceTargetDoesNotLabelSourceIndexAsUUID(t *testing.T) {
	target := identityEntryTarget(dcgmsource.GPUIdentityEntry{Index: 7})
	if target.GetGpu().GetUuid() != "" || target.GetGpu().GetSourceLocalId() != "dcgm-index-7" {
		t.Fatalf("device target = %v", target)
	}
}

func TestAppendNoTargetObservations(t *testing.T) {
	sensor := &sensor{source: dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})}
	selected := collect.Signals{gpusignals.PowerDraw, gpusignals.TemperatureCelsius}
	batch := &observation.ObservationBatch{}

	sensor.appendNoTargetObservations(batch, selected, time.Now())

	if len(batch.GetObservations()) != len(selected) {
		t.Fatalf("observations = %d, want %d", len(batch.GetObservations()), len(selected))
	}
	for index, current := range batch.GetObservations() {
		if current.GetSignalId() != selected[index].ID() {
			t.Errorf("observation %d signal = %q, want %q", index, current.GetSignalId(), selected[index].ID())
		}
		if current.GetTarget() != nil {
			t.Errorf("observation %d target = %v, want nil", index, current.GetTarget())
		}
		if current.GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
			t.Errorf("observation %d reason = %v, want %v", index, current.GetNoValue().GetReason(), observation.NoValueReasonNoTargets)
		}
	}
}

func TestAppendFieldPreservesNoValue(t *testing.T) {
	sensor := &sensor{source: dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})}
	batch := &observation.ObservationBatch{}

	sensor.appendField(
		batch,
		gpusignals.NVLinkFabricRouteUnhealthy,
		observation.GPUTarget("GPU-0"),
		dcgmsource.FieldValue{NoValue: &observation.NoValue{
			Reason:       observation.NoValueReasonNotSupported,
			SourceStatus: "mask condition unsupported",
			Detail:       "fabric condition is not supported",
		}},
		time.Now(),
	)

	if len(batch.GetObservations()) != 1 {
		t.Fatalf("observations = %d, want 1", len(batch.GetObservations()))
	}
	noValue := batch.GetObservations()[0].GetNoValue()
	if noValue.GetReason() != observation.NoValueReasonNotSupported {
		t.Fatalf("reason = %v, want %v", noValue.GetReason(), observation.NoValueReasonNotSupported)
	}
	if noValue.GetSourceStatus() != "mask condition unsupported" {
		t.Errorf("source status = %q, want %q", noValue.GetSourceStatus(), "mask condition unsupported")
	}
}

func TestMeasureNoGPUsPreservesEmptyInventory(t *testing.T) {
	source := &fakeInventorySource{snapshot: &observation.GPUInventorySnapshot{
		Source:  dcgmsource.Name,
		Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{}},
	}}
	cycle, err := collect.NewCycle("no-gpus", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := (&sensor{source: source, signals: supportedSignals}).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()), collect.WithGPUInventory())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.GetObservations()) != 1 || batch.Observations[0].GetNoValue().GetReason() != observation.NoValueReasonNoTargets {
		t.Fatalf("no-target observations = %v", batch.GetObservations())
	}
	if len(batch.GetInventory().GetGpuSnapshots()) != 1 || batch.Inventory.GpuSnapshots[0].GetInventory() == nil {
		t.Fatalf("empty inventory missing: %v", batch.GetInventory())
	}
	if source.identityReads != 1 || source.inventoryReads != 1 || source.metricReads != 0 {
		t.Fatalf("reads = %+v", source)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
}

func TestMeasureMetricsWithoutUUID(t *testing.T) {
	source := &fakeInventorySource{identities: []dcgmsource.GPUIdentityEntry{{Index: 7, UUIDError: &observation.CollectionError{Operation: "identity", Category: observation.CollectionErrorCategoryUnavailable}}}}
	cycle, err := collect.NewCycle("metrics-without-uuid", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := (&sensor{source: source, signals: supportedSignals}).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(gpusignals.PowerDraw.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.GetObservations()) != 1 {
		t.Fatalf("observations = %v", batch.GetObservations())
	}
	current := batch.Observations[0]
	if current.GetValue() == nil || current.GetCollectionError() != nil || current.GetTarget().GetGpu().GetSourceLocalId() != "dcgm-index-7" || current.GetTarget().GetGpu().GetUuid() != "" {
		t.Fatalf("metric without UUID = %v", current)
	}
	if source.metricReads != 1 || source.inventoryReads != 0 || batch.GetInventory() != nil {
		t.Fatalf("reads = %+v", source)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
}
