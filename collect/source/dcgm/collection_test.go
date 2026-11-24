// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

func collectionIncidentReport() *observation.DCGMHealthReport {
	return &observation.DCGMHealthReport{
		Entities: []*observation.ObservationTarget{healthTestGPUTarget(0)},
		Incidents: []*observation.DCGMHealthIncident{{
			System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(),
			ErrorCode: 37, Message: "memory fault", Entity: healthTestGPUTarget(0),
		}},
		NativeGroupOverallHealth: observation.DCGMHealthResultFail.Enum(),
	}
}

func TestCollectSelectsOutputsIndependently(t *testing.T) {
	for flags := range 8 {
		request := CollectionRequest{Health: flags&2 != 0, Inventory: flags&4 != 0}
		if flags&1 != 0 {
			request.Metrics = []string{signalPowerDraw}
		}
		name := fmt.Sprintf("metrics=%t/health=%t/inventory=%t", len(request.Metrics) != 0, request.Health, request.Inventory)
		t.Run(name, func(t *testing.T) {
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: collectionIncidentReport()}}
			source := newSource(native, &sync.Mutex{})
			registerMetricAndHealth(t, source)
			t.Cleanup(func() { _ = source.Close() })
			cycle := newTestCycle(t, name)
			result := source.Collect(context.Background(), cycle, request)
			if result.DiscoveryError != nil || result.HealthError != nil {
				t.Fatalf("selected collection failed: %+v", result)
			}
			if (result.HealthReport != nil) != request.Health || (result.Inventory != nil) != request.Inventory {
				t.Fatalf("collection produced unrequested outputs: %+v", result)
			}
			if len(request.Metrics) != 0 {
				if len(result.Metrics) != 1 || result.Metrics[0].Identity.UUID != "GPU-0" || result.Metrics[0].Fields[signalPowerDraw].Value.GetIntValue() != 1 {
					t.Fatalf("metric result lost identity or value: %+v", result)
				}
			} else if len(result.Metrics) != 0 {
				t.Fatalf("unrequested metrics produced GPU results: %+v", result.Metrics)
			}
			if request.Health && (result.HealthReport.GetCollectionId() != cycle.CollectionID() || len(result.HealthReport.GetIncidents()) != 1 ||
				result.HealthReport.GetIncidents()[0].GetEntity().GetGpu().GetUuid() != "GPU-0") {
				t.Fatalf("health result lost identity, incident, or collection: %v", result.HealthReport)
			}
			if request.Inventory {
				entries := result.Inventory.GetInventory().GetEntries()
				if result.Inventory.GetCollectionError() != nil || len(entries) != 1 || entries[0].GetGpu().GetUuid() != "GPU-0" {
					t.Fatalf("inventory result = %v", result.Inventory)
				}
			}
			wantMetrics, wantHealth, wantInventory, wantDiscovery, wantInit := int32(0), int32(0), int32(0), int32(0), int32(0)
			if len(request.Metrics) != 0 {
				wantMetrics = 1
			}
			if request.Health {
				wantHealth = 1
			}
			if request.Inventory {
				wantInventory = 1
			}
			if len(request.Metrics) != 0 || request.Inventory {
				wantDiscovery = 1
			}
			if flags != 0 {
				wantInit = 1
			}
			if native.initCalls.Load() != wantInit || native.enumerationCalls.Load() != wantDiscovery || native.watchCalls.Load() != wantMetrics ||
				native.readCalls.Load() != wantMetrics || native.healthWatchCalls.Load() != wantHealth || native.health.readCalls.Load() != wantHealth ||
				native.inventoryCalls.Load() != wantInventory {
				t.Fatalf("unrequested native work: init=%d discovery=%d metric-watch=%d metrics=%d health-watch=%d health=%d inventory=%d",
					native.initCalls.Load(), native.enumerationCalls.Load(), native.watchCalls.Load(), native.readCalls.Load(), native.healthWatchCalls.Load(),
					native.health.readCalls.Load(), native.inventoryCalls.Load())
			}
		})
	}
}

func TestCollectReusesSessionAndWatchesAcrossRequests(t *testing.T) {
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: collectionIncidentReport()}}
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	request := CollectionRequest{Metrics: []string{signalPowerDraw}, Health: true, Inventory: true}
	for round := range 3 {
		result := source.Collect(context.Background(), newTestCycle(t, fmt.Sprintf("reuse-%d", round)), request)
		if result.DiscoveryError != nil || result.HealthError != nil || len(result.Metrics) != 1 || result.Metrics[0].Fields[signalPowerDraw].Value == nil ||
			result.HealthReport == nil || len(result.Inventory.GetInventory().GetEntries()) != 1 {
			t.Fatalf("request %d failed: %+v", round, result)
		}
	}
	if native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 || native.watchCalls.Load() != 1 || native.healthWatchCalls.Load() != 1 ||
		native.enumerationCalls.Load() != 3 || native.readCalls.Load() != 3 || native.health.readCalls.Load() != 3 || native.inventoryCalls.Load() != 3 {
		t.Fatal("collection requests replaced the shared session or watches")
	}
}

func TestCollectionAdmissionFailureStaysWithItsReader(t *testing.T) {
	for _, replacementSucceeds := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement succeeds=%t", replacementSucceeds), func(t *testing.T) {
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: collectionIncidentReport()}}
			firstError := errors.New("first collection could not initialize DCGM")
			native.initErr = firstError
			source := newSource(native, &sync.Mutex{})
			registerMetricAndHealth(t, source)
			t.Cleanup(func() { _ = source.Close() })
			ctx := context.Background()
			first := source.newCollectionReader(newTestCycle(t, "first-admission"))
			wantFailure := collectionError(firstError)
			checkFailure := func(operation string, failure *observation.CollectionError) {
				t.Helper()
				if failure == nil || failure.GetDetail() != wantFailure.GetDetail() || failure.GetCategory() != wantFailure.GetCategory() ||
					failure.GetSourceError() != wantFailure.GetSourceError() {
					t.Fatalf("%s lost the reader's admission failure: %v, want %v", operation, failure, &wantFailure)
				}
			}
			_, failure := first.gpuIndices(ctx)
			checkFailure("initial discovery", failure)
			if native.initCalls.Load() != 1 || source.session != nil {
				t.Fatal("failed admission established a session or retried initialization")
			}
			secondError := errors.New("second collection could not initialize DCGM")
			native.initErr = secondError
			if replacementSucceeds {
				native.initErr = nil
			}
			second := source.newCollectionReader(newTestCycle(t, "second-admission"))
			indices, secondFailure := second.gpuIndices(ctx)
			if replacementSucceeds {
				if secondFailure != nil || len(indices) != 1 || native.initCalls.Load() != 2 {
					t.Fatalf("second reader could not initialize its session: indices=%v failure=%v", indices, secondFailure)
				}
			} else if secondFailure.GetDetail() != secondError.Error() || native.initCalls.Load() != 2 {
				t.Fatalf("second reader did not retain its own failure: %v", secondFailure)
			}
			native.initErr = nil
			calls := func() []int32 {
				return []int32{
					native.initCalls.Load(), native.cleanupCalls.Load(), native.enumerationCalls.Load(), native.uuidCalls.Load(),
					native.readCalls.Load(), native.watchCalls.Load(), native.healthWatchCalls.Load(), native.health.readCalls.Load(), native.inventoryCalls.Load(),
				}
			}
			before := calls()
			checkFailure("metrics", first.readGPUMetrics(ctx, 0, []string{signalPowerDraw})[signalPowerDraw].CollectionError)
			_, failure = first.readHealth(ctx)
			checkFailure("health", failure)
			_, failure = first.gpuIndices(ctx)
			checkFailure("discovery", failure)
			_, failure = first.gpuIdentities(ctx)
			checkFailure("identities", failure)
			identities := first.gpuUUIDs(ctx, []uint{0})
			if len(identities) != 1 || identities[0].UUID != "" {
				t.Fatalf("failed reader acquired an identity: %v", identities)
			}
			checkFailure("UUID", identities[0].UUIDError)
			checkFailure("PCI address", identities[0].BusIDError)
			snapshot := first.gpuInventory(ctx, []GPUIdentityEntry{{Index: 0, UUID: "GPU-known"}})
			entries := snapshot.GetInventory().GetEntries()
			if len(entries) != 1 || entries[0].GetGpu().GetUuid() != "GPU-known" {
				t.Fatalf("failed inventory lost its caller-supplied identity: %v", snapshot)
			}
			checkFailure("inventory", entries[0].GetCollectionError())
			if after := calls(); !slices.Equal(before, after) {
				t.Fatalf("failed reader retried native work after another admission: before=%v after=%v", before, after)
			}
			if replacementSucceeds {
				if field := second.readGPUMetrics(ctx, 0, []string{signalPowerDraw})[signalPowerDraw]; field.Value == nil {
					t.Fatalf("first reader's failure affected the admitted reader: %v", field)
				}
			}
		})
	}
}

func TestCollectFeatureFailurePreservesOtherOutputs(t *testing.T) {
	for _, failedFeature := range []string{"metrics", "health"} {
		t.Run(failedFeature, func(t *testing.T) {
			native := &recoveryNative{healthLifecycleNative: &healthLifecycleNative{health: &fakeHealthWatch{report: collectionIncidentReport()}}}
			failure := errors.New("feature unavailable")
			if failedFeature == "metrics" {
				native.metricError = failure
			} else {
				native.health.readError = failure
			}
			source := newSource(native, &sync.Mutex{})
			registerMetricAndHealth(t, source)
			t.Cleanup(func() { _ = source.Close() })
			result := source.Collect(context.Background(), newTestCycle(t, failedFeature), CollectionRequest{
				Metrics: []string{signalPowerDraw}, Health: true, Inventory: true,
			})
			if result.DiscoveryError != nil || len(result.Metrics) != 1 || len(result.Inventory.GetInventory().GetEntries()) != 1 ||
				result.Inventory.GetInventory().GetEntries()[0].GetCollectionError() != nil {
				t.Fatalf("feature failure discarded discovery or inventory: %+v", result)
			}
			metric := result.Metrics[0].Fields[signalPowerDraw]
			if failedFeature == "metrics" {
				if metric.CollectionError == nil || result.HealthError != nil || result.HealthReport == nil {
					t.Fatalf("metric failure discarded health: %+v", result)
				}
			} else if metric.Value == nil || result.HealthReport != nil || result.HealthError.GetDetail() != failure.Error() {
				t.Fatalf("health failure discarded metrics: %+v", result)
			}
		})
	}
}

func TestCollectionReadersDoNotCrossSessionRecovery(t *testing.T) {
	for _, failedRead := range []string{"metric call", "metric field", "health call", "health system"} {
		t.Run(failedRead, func(t *testing.T) {
			native := &recoveryNative{healthLifecycleNative: &healthLifecycleNative{health: &fakeHealthWatch{report: collectionIncidentReport()}}}
			native.gpuUUIDs = map[uint]string{0: "GPU-old"}
			source := newSource(native, &sync.Mutex{})
			registerMetricAndHealth(t, source)
			t.Cleanup(func() { _ = source.Close() })
			ctx := context.Background()
			first := source.newCollectionReader(newTestCycle(t, "first"))
			identities, failure := first.gpuIdentities(ctx)
			if failure != nil || len(identities) != 1 || identities[0].UUID != "GPU-old" {
				t.Fatalf("first identity lookup failed: %v, %v", identities, failure)
			}
			values := first.readGPUMetrics(ctx, 0, []string{signalPowerDraw})
			report, failure := first.readHealthReport(ctx)
			if failure != nil || values[signalPowerDraw].Value == nil || len(report.GetIncidents()) != 1 {
				t.Fatalf("first collection failed before interleaving: metrics=%v report=%v failure=%v", values, report, failure)
			}
			second := source.newCollectionReader(newTestCycle(t, "second"))
			if field := second.readGPUMetrics(ctx, 0, []string{signalPowerDraw})[signalPowerDraw]; field.Value == nil || first.session != second.session {
				t.Fatalf("healthy readers did not share the session: %v", field)
			}
			oldSession := first.session
			connectionFailure := &observation.CollectionError{
				Category: observation.CollectionErrorCategoryUnavailable, SourceError: connectionNotValidStatus, Detail: "connection lost in field",
			}
			switch failedRead {
			case "metric call":
				native.metricError = connectionLoss()
				_ = second.readGPUMetrics(ctx, 0, []string{signalPowerDraw})
			case "metric field":
				native.metricValues = map[string]FieldValue{signalPowerDraw: {CollectionError: connectionFailure}}
				_ = second.readGPUMetrics(ctx, 0, []string{signalPowerDraw})
			case "health call":
				native.health.readError = connectionLoss()
				_, _ = second.readHealthReport(ctx)
			case "health system":
				native.health.report = &observation.DCGMHealthReport{SystemErrors: map[uint64]*observation.CollectionError{dcgmhealth.SystemMemory: connectionFailure}}
				_, _ = second.readHealthReport(ctx)
			}
			if oldSession.connectionFailure == nil {
				t.Fatal("the failing reader did not invalidate its session")
			}
			beforeUUID, beforeMetrics := native.uuidCalls.Load(), native.readCalls.Load()
			first.enrichHealthIdentities(ctx, report)
			if field := first.readGPUMetrics(ctx, 0, []string{signalPowerDraw})[signalPowerDraw]; field.CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable {
				t.Fatalf("old collection entered a failed session: %v", field)
			}
			if native.initCalls.Load() != 1 || native.uuidCalls.Load() != beforeUUID || native.readCalls.Load() != beforeMetrics {
				t.Fatal("an already-bound collection retried or reconnected")
			}
			native.metricError, native.metricValues = nil, nil
			native.health = &fakeHealthWatch{report: collectionIncidentReport()}
			native.gpuUUIDs = map[uint]string{0: "GPU-new"}
			fresh := source.newCollectionReader(newTestCycle(t, "fresh"))
			freshIdentities, failure := fresh.gpuIdentities(ctx)
			if failure != nil || len(freshIdentities) != 1 || freshIdentities[0].UUID != "GPU-new" || fresh.session == oldSession {
				t.Fatalf("fresh collection did not recover a replacement session: %v, %v", freshIdentities, failure)
			}
			beforeUUID, beforeMetrics, beforeInventory := native.uuidCalls.Load(), native.readCalls.Load(), native.inventoryCalls.Load()
			first.enrichHealthIdentities(ctx, report)
			if field := first.readGPUMetrics(ctx, 0, []string{signalPowerDraw})[signalPowerDraw]; field.CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable ||
				field.CollectionError.GetSourceError() != connectionNotValidStatus {
				t.Fatalf("stale collection lost the originating session error: %v", field)
			}
			staleInventory := first.gpuInventory(ctx, identities)
			entries := staleInventory.GetInventory().GetEntries()
			if len(entries) != 1 || entries[0].GetGpu().GetUuid() != "GPU-old" || entries[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
				t.Fatalf("stale inventory joined replacement attributes: %v", staleInventory)
			}
			if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.uuidCalls.Load() != beforeUUID ||
				native.readCalls.Load() != beforeMetrics || native.inventoryCalls.Load() != beforeInventory || source.session != fresh.session || fresh.session.connectionFailure != nil {
				t.Fatal("stale collection touched or invalidated the replacement session")
			}
			if identities[0].UUID != "GPU-old" || values[signalPowerDraw].Value.GetIntValue() != 1 || values[signalPowerDraw].CollectionError != nil ||
				len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetErrorCode() != 37 || report.GetIncidents()[0].GetEntity().GetGpu().GetUuid() != "" {
				t.Fatalf("completed old-session results were changed: identities=%v metrics=%v report=%v", identities, values, report)
			}
			if field := fresh.readGPUMetrics(ctx, 0, []string{signalPowerDraw})[signalPowerDraw]; field.Value == nil {
				t.Fatalf("stale reader poisoned the fresh collection: %v", field)
			}
		})
	}
}

type collectionOrderNative struct {
	*healthLifecycleNative
	metricStarted chan struct{}
	releaseMetric chan struct{}
	calls         []string
	blockedGPU    uint
}

func (native *collectionOrderNative) AllGPUIndices() ([]uint, error) {
	native.calls = append(native.calls, "discovery")
	return native.healthLifecycleNative.AllGPUIndices()
}

func (native *collectionOrderNative) ReadGPUUUIDs(ids []uint) (map[uint]GPUIdentityEntry, error) {
	native.calls = append(native.calls, "identity")
	return native.healthLifecycleNative.ReadGPUUUIDs(ids)
}

func (native *collectionOrderNative) ReadGPUMetrics(gpu uint, signals []string) (map[string]FieldValue, error) {
	native.calls = append(native.calls, fmt.Sprintf("metrics %d", gpu))
	native.readCalls.Add(1)
	if native.metricStarted != nil && gpu == native.blockedGPU {
		close(native.metricStarted)
		<-native.releaseMetric
	}
	values := make(map[string]FieldValue, len(signals))
	for _, signal := range signals {
		values[signal] = FieldValue{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 1}}}
	}
	return values, nil
}

func (native *collectionOrderNative) WatchHealth(systems uint64) (healthWatch, error) {
	native.calls = append(native.calls, "health")
	return native.healthLifecycleNative.WatchHealth(systems)
}

func (native *collectionOrderNative) ReadGPUInventory(ids []uint) (map[uint]*observation.GPUInventoryEntry, error) {
	native.calls = append(native.calls, "inventory")
	return native.healthLifecycleNative.ReadGPUInventory(ids)
}

func TestCollectReadsHealthBeforeInventoryOnlyDiscovery(t *testing.T) {
	native := &collectionOrderNative{healthLifecycleNative: &healthLifecycleNative{health: &fakeHealthWatch{report: collectionIncidentReport()}}}
	native.enumerationErr = errors.New("inventory discovery unavailable")
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	result := source.Collect(context.Background(), newTestCycle(t, "health-before-inventory"), CollectionRequest{Health: true, Inventory: true})
	if result.HealthReport == nil || result.HealthError != nil || len(result.HealthReport.GetIncidents()) != 1 || result.DiscoveryError == nil ||
		result.Inventory.GetCollectionError() == nil || !slices.Equal(native.calls, []string{"health", "identity", "discovery"}) {
		t.Fatalf("inventory discovery discarded or preceded health: result=%+v calls=%v", result, native.calls)
	}
}

func TestCollectMetricTimeoutKeepsEarlierIdentityAndValue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := &sync.Mutex{}
		native := &collectionOrderNative{
			healthLifecycleNative: &healthLifecycleNative{}, metricStarted: make(chan struct{}), releaseMetric: make(chan struct{}), blockedGPU: 1,
		}
		native.gpuIndices = []uint{0, 1}
		release := sync.OnceFunc(func() { close(native.releaseMetric) })
		defer release()
		source := newSource(native, gate)
		registerMetricAndHealth(t, source)
		t.Cleanup(func() { _ = source.Close() })
		const timeout = 100 * time.Millisecond
		cycle, err := collect.NewCycleWithTimeout("partial-metrics", time.Now(), timeout)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan CollectionResult, 1)
		go func() {
			finished <- source.Collect(context.Background(), cycle, CollectionRequest{Metrics: []string{signalPowerDraw}})
		}()
		synctest.Wait()
		select {
		case <-native.metricStarted:
		default:
			t.Fatal("second GPU's native read did not start")
		}
		time.Sleep(timeout)
		result := <-finished
		if result.DiscoveryError != nil || len(result.Metrics) != 2 || result.Metrics[0].Identity.UUID != "GPU-0" ||
			result.Metrics[0].Fields[signalPowerDraw].Value.GetIntValue() != 1 || result.Metrics[0].Fields[signalPowerDraw].CollectionError != nil ||
			result.Metrics[1].Identity.UUID != "GPU-1" || result.Metrics[1].Fields[signalPowerDraw].CollectionError.GetCategory() != observation.CollectionErrorCategoryTimeout {
			t.Fatalf("later timeout discarded earlier results: %+v", result)
		}
		if !slices.Equal(native.calls, []string{"discovery", "identity", "metrics 0", "metrics 1"}) {
			t.Fatalf("metric reads did not use identities established first: %v", native.calls)
		}
		if fields := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "while-native-blocked"), 0, []string{signalPowerDraw}); fields[signalPowerDraw].CollectionError == nil || native.readCalls.Load() != 2 {
			t.Fatalf("another collection entered unfinished native work: %v", fields)
		}
		if closeErr := source.Close(); !errors.Is(closeErr, ErrInUse) || native.cleanupCalls.Load() != 0 {
			t.Fatalf("Close released a session with native work active: %v", closeErr)
		}
		release()
		synctest.Wait()
		if closeErr := source.Close(); closeErr != nil || native.cleanupCalls.Load() != 1 {
			t.Fatalf("finished native work retained client ownership: %v", closeErr)
		}
	})
}
