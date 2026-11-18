// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

type fakeInventorySource struct {
	snapshot           *observation.GPUInventorySnapshot
	enumerationFailure *observation.CollectionError
	values             map[string]dcgmsource.FieldValue
	identities         []dcgmsource.GPUIdentityEntry
	inventoryReads     int
	enumerationReads   int
	metricReads        int
	uuidReads          int
}

func (*fakeInventorySource) ID() string { return dcgmsource.Name }

func (source *fakeInventorySource) GPUInventory(context.Context, *collect.Cycle, []dcgmsource.GPUIdentityEntry) *observation.GPUInventorySnapshot {
	source.inventoryReads++
	return source.snapshot
}

func (source *fakeInventorySource) GPUIndices(context.Context, *collect.Cycle) ([]uint, *observation.CollectionError) {
	source.enumerationReads++
	if source.enumerationFailure != nil {
		return nil, source.enumerationFailure
	}
	indices := make([]uint, 0, len(source.identities))
	for _, entry := range source.identities {
		indices = append(indices, entry.Index)
	}
	return indices, nil
}

func (source *fakeInventorySource) GPUUUIDs(_ context.Context, _ *collect.Cycle, indices []uint) []dcgmsource.GPUIdentityEntry {
	source.uuidReads++
	entries := make([]dcgmsource.GPUIdentityEntry, 0, len(indices))
	for _, index := range indices {
		for _, entry := range source.identities {
			if entry.Index == index {
				entries = append(entries, entry)
				break
			}
		}
	}
	return entries
}

func (source *fakeInventorySource) ReadGPUMetrics(_ context.Context, _ *collect.Cycle, _ uint, signalIDs []string) map[string]dcgmsource.FieldValue {
	source.metricReads++
	if source.values != nil {
		return source.values
	}
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
	if len(batch.GetObservations()) != 0 || source.inventoryReads != 1 || source.enumerationReads != 1 || source.metricReads != 0 {
		t.Fatalf("inventory-only collection: observations=%d inventory reads=%d identity reads=%d metric reads=%d", len(batch.GetObservations()), source.inventoryReads, source.enumerationReads, source.metricReads)
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
	if batch.GetInventory() != nil || len(batch.GetObservations()) != 0 || source.inventoryReads != 0 || source.enumerationReads != 0 {
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
	if len(batch.GetObservations()) != 1 || source.inventoryReads != 0 || source.enumerationReads != 1 || source.metricReads != 1 {
		t.Fatalf("metrics-only collection: observations=%d inventory reads=%d identity reads=%d metric reads=%d", len(batch.GetObservations()), source.inventoryReads, source.enumerationReads, source.metricReads)
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
	if batch.GetInventory() == nil || len(batch.GetObservations()) != 1 || source.inventoryReads != 1 || source.enumerationReads != 1 || source.metricReads != 1 {
		t.Fatalf("combined collection: observations=%d inventory reads=%d identity reads=%d metric reads=%d", len(batch.GetObservations()), source.inventoryReads, source.enumerationReads, source.metricReads)
	}
}

func TestResolveMetricSignals(t *testing.T) {
	t.Run("defaults to every signal", func(t *testing.T) {
		resolved, err := resolveMetricSignals(nil)
		if err != nil {
			t.Fatalf("resolveMetricSignals() error = %v", err)
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
		resolved, err := resolveMetricSignals(requested)
		if err != nil {
			t.Fatalf("resolveMetricSignals() error = %v", err)
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
		if _, err := resolveMetricSignals([]string{"gpu.unknown"}); err == nil {
			t.Fatal("resolveMetricSignals() returned nil error")
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
		source := &fakeInventorySource{enumerationFailure: &observation.CollectionError{Operation: "identity", Category: observation.CollectionErrorCategoryUnavailable}}
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
		if source.enumerationReads != 1 || source.inventoryReads != 0 || source.metricReads != 0 {
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
		if source.enumerationReads != 1 || source.inventoryReads != 1 || source.metricReads != 2 {
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
	if source.enumerationReads != 1 || source.inventoryReads != 1 || source.metricReads != 0 {
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
	native := current.GetTarget().GetGpu().GetNative()
	if native.GetSource() != dcgmsource.Name || native.GetEntityKind() != dcgmGPUEntityKind || native.GetEntityId() != 7 {
		t.Fatalf("metric without UUID lost native identity: %v", native)
	}
	if source.metricReads != 1 || source.inventoryReads != 0 || batch.GetInventory() != nil {
		t.Fatalf("reads = %+v", source)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
}

type blockingUUIDSource struct {
	*fakeInventorySource
	uuidStarted chan struct{}
	release     chan struct{}
}

func (source *blockingUUIDSource) ReadGPUMetrics(ctx context.Context, cycle *collect.Cycle, index uint, ids []string) map[string]dcgmsource.FieldValue {
	values, err := collect.Run(ctx, cycle, dcgmsource.Name, func(callContext context.Context) (map[string]dcgmsource.FieldValue, error) {
		return source.fakeInventorySource.ReadGPUMetrics(callContext, cycle, index, ids), nil
	})
	if err == nil {
		return values
	}
	values = make(map[string]dcgmsource.FieldValue, len(ids))
	for _, id := range ids {
		values[id] = dcgmsource.FieldValue{CollectionError: &observation.CollectionError{
			Category: observation.CollectionErrorCategoryUnavailable, Detail: err.Error(),
		}}
	}
	return values
}

func (source *blockingUUIDSource) GPUUUIDs(ctx context.Context, cycle *collect.Cycle, indices []uint) []dcgmsource.GPUIdentityEntry {
	source.uuidReads++
	_, err := collect.Run(ctx, cycle, dcgmsource.Name, func(context.Context) (struct{}, error) {
		close(source.uuidStarted)
		<-source.release
		return struct{}{}, nil
	})
	entries := make([]dcgmsource.GPUIdentityEntry, 0, len(indices))
	for _, index := range indices {
		entry := dcgmsource.GPUIdentityEntry{Index: index}
		if err != nil {
			entry.UUIDError = &observation.CollectionError{
				Category: observation.CollectionErrorCategoryUnavailable, Detail: err.Error(),
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestMeasureMetricValuesSurviveUUIDCancellation(t *testing.T) {
	source := &blockingUUIDSource{
		fakeInventorySource: &fakeInventorySource{identities: []dcgmsource.GPUIdentityEntry{{Index: 2}, {Index: 7}}},
		uuidStarted:         make(chan struct{}),
		release:             make(chan struct{}),
	}
	t.Cleanup(sync.OnceFunc(func() { close(source.release) }))
	cycle, err := collect.NewCycle("metrics-before-uuid-cancellation", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sensor := &sensor{source: source, signals: collect.Signals{gpusignals.PowerDraw, gpusignals.TemperatureCelsius}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		err   error
		batch *observation.ObservationBatch
	}
	completed := make(chan result, 1)
	go func() {
		batch, measureErr := sensor.Measure(ctx, collect.WithCycle(cycle))
		completed <- result{err: measureErr, batch: batch}
	}()
	select {
	case <-source.uuidStarted:
	case early := <-completed:
		t.Fatalf("Measure returned before UUID lookup started: %v", early.err)
	case <-time.After(5 * time.Second):
		t.Fatal("UUID lookup did not start")
	}
	cancel()
	var measured result
	select {
	case measured = <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("Measure did not return after UUID cancellation")
	}
	if measured.err != nil {
		t.Fatal(measured.err)
	}
	batch := measured.batch
	if source.metricReads != 2 || source.uuidReads != 1 || len(batch.Observations) != 4 {
		t.Fatalf("UUID cancellation suppressed metric reads: reads=%d UUID reads=%d batch=%v", source.metricReads, source.uuidReads, batch)
	}
	for position, current := range batch.Observations {
		gpu := current.GetTarget().GetGpu()
		wantIndex := uint32(2)
		if position >= 2 {
			wantIndex = 7
		}
		if current.GetValue().GetDoubleValue() != 100 || current.GetCollectionError() != nil || gpu.GetUuid() != "" ||
			gpu.GetNative().GetSource() != dcgmsource.Name || gpu.GetNative().GetEntityId() != wantIndex {
			t.Fatalf("metric lost its value or native identity: %v", current)
		}
	}
	if err := observation.ValidateBatch(batch); err != nil {
		t.Fatal(err)
	}
}

func TestNewAcceptsEveryDCGMSourceMode(t *testing.T) {
	for name, source := range map[string]*dcgmsource.Source{
		"standalone": dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{}),
		"embedded":   dcgmsource.NewEmbeddedSource(dcgmsource.EmbeddedOptions{}),
	} {
		defaults, err := New(source)
		if err != nil {
			t.Fatalf("New(%s) defaults error = %v", name, err)
		}
		if !slices.Equal(defaults.Signals(), supportedSignals) {
			t.Fatalf("New(%s) default signals = %v, want metrics only", name, defaults.Signals().IDs())
		}
		if _, err := New(source, gpusignals.PowerDraw.ID(), gpusignals.TemperatureCelsius.ID()); err != nil {
			t.Fatalf("New(%s) error = %v", name, err)
		}
	}
}

// The DCGM source reports a first sample with no timestamp as a NO_DATA field
// error, and a field DCGM omitted entirely must not read as healthy either.
func TestMeasureIncompleteFirstSampleFailsClosed(t *testing.T) {
	noTimestamp := &observation.CollectionError{
		Source:      dcgmsource.Name,
		Category:    observation.CollectionErrorCategoryUnavailable,
		SourceError: "DCGM_ST_NO_DATA",
		Detail:      "DCGM signal has no sample timestamp",
	}
	sampledAt := time.Unix(1_700_000_000, 0).UTC()
	source := &fakeInventorySource{
		identities: []dcgmsource.GPUIdentityEntry{{Index: 0, UUID: "GPU-0"}},
		values: map[string]dcgmsource.FieldValue{
			gpusignals.PowerDraw.ID():          {CollectionError: noTimestamp},
			gpusignals.PowerLimitEnforced.ID(): {ObservedAt: sampledAt, Value: &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: 700}}},
		},
	}
	startedAt := time.Unix(1_700_000_100, 0).UTC()
	cycle, err := collect.NewCycle("incomplete-first-sample", startedAt)
	if err != nil {
		t.Fatal(err)
	}
	selected := []string{gpusignals.PowerDraw.ID(), gpusignals.PowerLimitEnforced.ID(), gpusignals.PerformanceState.ID()}
	batch, err := (&sensor{source: source, signals: supportedSignals}).Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(selected...))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		t.Fatal(validationErr)
	}
	got := make(map[string]*observation.Observation, len(batch.GetObservations()))
	for _, current := range batch.GetObservations() {
		if current.GetTarget().GetGpu().GetUuid() != "GPU-0" {
			t.Fatalf("observation target = %v, want GPU-0", current.GetTarget())
		}
		got[current.GetSignalId()] = current
	}
	if len(got) != len(selected) {
		t.Fatalf("observations = %v, want one per selected signal", batch.GetObservations())
	}

	untimed := got[gpusignals.PowerDraw.ID()]
	if untimed.GetValue() != nil || untimed.GetCollectionError().GetSourceError() != "DCGM_ST_NO_DATA" {
		t.Errorf("sample without timestamp = %v, want the source's NO_DATA collection error", untimed)
	}
	if !untimed.GetObservedAt().AsTime().Equal(startedAt) {
		t.Errorf("sample without timestamp observed at %v, want cycle start %v", untimed.GetObservedAt().AsTime(), startedAt)
	}

	missing := got[gpusignals.PerformanceState.ID()]
	if missing.GetValue() != nil || missing.GetCollectionError().GetCategory() != observation.CollectionErrorCategorySourceError || missing.GetCollectionError().GetOperation() != "metrics" {
		t.Errorf("field missing from DCGM response = %v, want a metrics source error", missing)
	}

	healthy := got[gpusignals.PowerLimitEnforced.ID()]
	if healthy.GetValue().GetDoubleValue() != 700 || !healthy.GetObservedAt().AsTime().Equal(sampledAt) {
		t.Errorf("sampled field = %v, want 700 at its sample time", healthy)
	}
}
