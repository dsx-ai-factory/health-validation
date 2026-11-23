// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

func healthTestGPUTarget(id uint32) *observation.ObservationTarget {
	return &observation.ObservationTarget{Kind: &observation.ObservationTarget_GPU{Gpu: &observation.GPURef{
		Native: &observation.NativeEntityRef{Source: Name, EntityKind: 1, EntityId: id},
	}}}
}

type fakeHealthWatch struct {
	readError          error
	cleanupError       error
	report             *observation.DCGMHealthReport
	readStarted        chan struct{}
	releaseRead        chan struct{}
	native             *fakeNative
	gate               *sync.Mutex
	readCalls          atomic.Int32
	cleanupCalls       atomic.Int32
	outsideGate        atomic.Bool
	cleanedAfterClient atomic.Bool
	missingReport      bool
}

func (watch *fakeHealthWatch) recordGate() {
	if watch.gate != nil && watch.gate.TryLock() {
		watch.outsideGate.Store(true)
		watch.gate.Unlock()
	}
}

func (watch *fakeHealthWatch) Read() (*observation.DCGMHealthReport, error) {
	watch.recordGate()
	watch.readCalls.Add(1)
	if watch.readStarted != nil {
		close(watch.readStarted)
		<-watch.releaseRead
	}
	if watch.readError != nil {
		return nil, watch.readError
	}
	if watch.report == nil && !watch.missingReport {
		return &observation.DCGMHealthReport{}, nil
	}
	return watch.report, nil
}

func (watch *fakeHealthWatch) Close() error {
	watch.recordGate()
	watch.cleanupCalls.Add(1)
	if watch.native != nil && watch.native.cleanupCalls.Load() != 0 {
		watch.cleanedAfterClient.Store(true)
	}
	return watch.cleanupError
}

type healthLifecycleNative struct {
	healthSetupError error
	health           *fakeHealthWatch
	cleanupNative
	healthSystems    uint64
	healthWatchCalls atomic.Int32
	missingWatch     bool
}

func (native *healthLifecycleNative) WatchHealth(systems uint64) (healthWatch, error) {
	native.healthWatchCalls.Add(1)
	native.healthSystems = systems
	if native.missingWatch {
		return nil, nil
	}
	if native.healthSetupError != nil {
		return nil, native.healthSetupError
	}
	if native.health == nil {
		native.health = &fakeHealthWatch{}
	}
	native.health.native = &native.fakeNative
	native.health.gate = native.nativeCallGate
	return native.health, nil
}

func registerMetricAndHealth(t *testing.T, source *Source) {
	t.Helper()
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
}

func TestAddHealthToWatchValidatesBeforeChangingUnion(t *testing.T) {
	source := newSource(&fakeNative{}, &sync.Mutex{})
	for _, invalid := range []uint64{1 << 63, dcgmhealth.SystemMemory | 1<<63, dcgmhealth.SystemAll} {
		if err := source.AddHealthToWatch(invalid); err == nil || source.healthSystems != 0 {
			t.Fatalf("AddHealthToWatch(%v) = %v, systems = %d", invalid, err, source.healthSystems)
		}
	}
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe); err != nil {
		t.Fatal(err)
	}
	if err := source.AddHealthToWatch(dcgmhealth.SystemNVSwitchFatal); err != nil {
		t.Fatal(err)
	}
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
	if err := source.AddHealthToWatch(dcgmhealth.SystemPower | 1<<63); err == nil {
		t.Fatal("mixed valid and unknown systems were accepted")
	}
	if want := dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe | dcgmhealth.SystemNVSwitchFatal; source.healthSystems != want {
		t.Fatalf("systems = %d, want %d", source.healthSystems, want)
	}
}

func TestAddHealthToWatchZeroKeepsUnionWithoutNativeWork(t *testing.T) {
	for _, test := range []struct {
		name    string
		systems uint64
	}{
		{name: "empty"},
		{name: "registered", systems: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := &healthLifecycleNative{}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(test.systems); err != nil {
				t.Fatal(err)
			}
			if err := source.AddHealthToWatch(0); err != nil {
				t.Fatal(err)
			}
			if source.healthSystems != test.systems || native.initCalls.Load() != 0 || native.healthWatchCalls.Load() != 0 || native.watchCalls.Load() != 0 {
				t.Fatal("zero registration changed the requested union or performed native work")
			}
			result := source.Collect(context.Background(), newTestCycle(t, "zero-"+test.name), CollectionRequest{Health: true})
			report, failure := result.HealthReport, result.HealthError
			if test.systems == 0 {
				if report != nil || failure.GetCategory() != observation.CollectionErrorCategoryNotConfigured || native.initCalls.Load() != 0 || native.healthWatchCalls.Load() != 0 {
					t.Fatalf("disabled health read = %v, %v; initialized=%d watches=%d", report, failure, native.initCalls.Load(), native.healthWatchCalls.Load())
				}
			} else if failure != nil || report == nil || native.healthSystems != test.systems || native.healthWatchCalls.Load() != 1 {
				t.Fatalf("registered health read = %v, %v; native systems=%#x", report, failure, native.healthSystems)
			}
		})
	}
}

func TestMetricCollectionDoesNotEnableHealth(t *testing.T) {
	native := &healthLifecycleNative{}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	if values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "metric-only"), 0, []string{signalPowerDraw}); values[signalPowerDraw].Value == nil {
		t.Fatalf("metric-only read = %v", values)
	}
	if failure := source.Collect(context.Background(), newTestCycle(t, "no-health"), CollectionRequest{Health: true}).HealthError; failure.GetCategory() != observation.CollectionErrorCategoryNotConfigured {
		t.Fatalf("unregistered health = %v", failure)
	}
	if native.healthWatchCalls.Load() != 0 || native.initCalls.Load() != 1 {
		t.Fatalf("health setup = %d, client initialization = %d", native.healthWatchCalls.Load(), native.initCalls.Load())
	}
}

func TestHealthAndMetricRegistrationShareStartedBoundary(t *testing.T) {
	for _, stage := range []string{"metric", "health"} {
		t.Run(stage, func(t *testing.T) {
			source := newSource(&fakeNative{}, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			registerMetricAndHealth(t, source)
			cycle := newTestCycle(t, "freeze-"+stage)
			switch stage {
			case "metric":
				if values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw}); values[signalPowerDraw].Value == nil {
					t.Fatal(values)
				}
			case "health":
				if failure := source.Collect(context.Background(), cycle, CollectionRequest{Health: true}).HealthError; failure != nil {
					t.Fatal(failure)
				}
			}
			if source.session != nil && source.session.metricWatchCleanup != nil && stage != "metric" {
				t.Fatal("non-metric read eagerly created metric watch")
			}
			if err := source.AddMetricsToWatch([]string{signalTemperature}); err == nil {
				t.Fatal("metric registration succeeded after collection started")
			}
			if err := source.AddHealthToWatch(dcgmhealth.SystemPCIe); err == nil {
				t.Fatal("health registration succeeded after collection started")
			}
			if err := source.AddHealthToWatch(0); err == nil {
				t.Fatal("zero health registration bypassed the started boundary")
			}
		})
	}
}

func TestInventoryInitializationKeepsWatchRegistrationOpen(t *testing.T) {
	for _, stage := range []string{"successful", "failed"} {
		t.Run(stage, func(t *testing.T) {
			native := &healthLifecycleNative{}
			if stage == "failed" {
				native.initErr = errNoHostengine
			}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			cycle := newTestCycle(t, "inventory-init")
			identities, failure := source.GPUIdentities(context.Background(), cycle)
			if (failure != nil) != (stage == "failed") {
				t.Fatalf("inventory initialization = %v", failure)
			}
			if failure == nil {
				if entries := source.GPUInventory(context.Background(), cycle, identities).GetInventory().GetEntries(); len(entries) != 1 || entries[0].GetCollectionError() != nil {
					t.Fatalf("inventory = %v", entries)
				}
			}
			registerMetricAndHealth(t, source)
			if native.watchCalls.Load() != 0 || native.healthWatchCalls.Load() != 0 {
				t.Fatal("inventory or registration eagerly created watches")
			}
			native.initErr = nil
			if report, failure := source.newCollectionReader(newTestCycle(t, "health-after-inventory")).readHealth(context.Background()); failure != nil || report.GetRequestedSystems() != dcgmhealth.SystemMemory || native.healthSystems != dcgmhealth.SystemMemory {
				t.Fatalf("health registered after inventory = %v, %v", report, failure)
			}
			wantInitCalls := int32(1)
			if stage == "failed" {
				wantInitCalls++
			}
			if native.initCalls.Load() != wantInitCalls || native.healthWatchCalls.Load() != 1 {
				t.Fatal("health did not reuse the initialized client or retry failed initialization")
			}
		})
	}
}

func TestHealthEmptyReportReturnsImmediatelyAndKeepsWatchTimestamp(t *testing.T) {
	monitoredSince := timestamppb.New(time.Unix(100, 0))
	raw := &observation.DCGMHealthReport{
		GroupId: proto.Uint64(23), NativeGroupOverallHealth: observation.DCGMHealthResultPass.Enum(),
		Entities:              []*observation.ObservationTarget{healthTestGPUTarget(2)},
		MonitoredSince:        monitoredSince,
		SystemsWithoutTargets: dcgmhealth.SystemNVSwitchFatal,
	}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: raw}}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	systems := dcgmhealth.SystemMemory | dcgmhealth.SystemNVSwitchFatal
	if err := source.AddHealthToWatch(systems); err != nil {
		t.Fatal(err)
	}
	if native.initCalls.Load() != 0 || native.healthWatchCalls.Load() != 0 {
		t.Fatal("health registration eagerly initialized the native watch")
	}
	for _, cycleID := range []string{"first-empty-health", "later-empty-health"} {
		cycle := newTestCycle(t, cycleID)
		result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
		report, failure := result.HealthReport, result.HealthError
		if failure != nil || report == nil || len(report.GetIncidents()) != 0 {
			t.Fatalf("empty health report = %v, %v", report, failure)
		}
		if report.GetGroupId() != 23 || report.GetNativeGroupOverallHealth() != observation.DCGMHealthResultPass ||
			report.GetRequestedSystems() != systems || report.GetSystemsWithoutTargets() != dcgmhealth.SystemNVSwitchFatal ||
			report.GetEntities()[0].GetGpu().GetUuid() != "GPU-2" || report.GetEntities()[0].GetGpu().GetNative().GetEntityId() != 2 ||
			report.GetCollectionId() != cycle.CollectionID() || !proto.Equal(report.GetMonitoredSince(), monitoredSince) {
			t.Fatalf("empty health report lost metadata: %v", report)
		}
	}
	if native.healthWatchCalls.Load() != 1 || native.health.readCalls.Load() != 2 || native.initCalls.Load() != 1 {
		t.Fatal("repeated empty reads did not retain the original health watch")
	}
	if !proto.Equal(raw.GetMonitoredSince(), monitoredSince) || raw.GetCollectionId() != "" || raw.GetEntities()[0].GetGpu().GetUuid() != "" {
		t.Fatal("empty health read mutated the native-owned report")
	}
}

func TestHealthReportMetadataAndSharedClient(t *testing.T) {
	gate := &sync.Mutex{}
	monitoredSince := time.Unix(100, 0)
	raw := &observation.DCGMHealthReport{
		GroupId: proto.Uint64(23), RequestedSystems: 999, NativeGroupOverallHealth: observation.DCGMHealthResult(987).Enum(),
		Entities:       []*observation.ObservationTarget{{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 51, EntityId: 17}}}},
		Incidents:      []*observation.DCGMHealthIncident{{System: 9999, NativeHealth: observation.DCGMHealthResult(777).Enum(), ErrorCode: 1234, Message: "native detail"}},
		MonitoredSince: timestamppb.New(monitoredSince),
		SystemErrors: map[uint64]*observation.CollectionError{
			dcgmhealth.SystemPCIe: {Source: Name, Operation: "health discovery", Category: observation.CollectionErrorCategoryUnavailable, Detail: "PCIe discovery failed"},
		},
		PossiblyTruncated: true,
	}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: raw}, cleanupNative: cleanupNative{nativeCallGate: gate}}
	source := newSource(native, gate)
	t.Cleanup(func() { _ = source.Close() })
	registerMetricAndHealth(t, source)
	if err := source.AddHealthToWatch(dcgmhealth.SystemPCIe); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "shared-cycle")
	values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
	if values[signalPowerDraw].Value == nil || native.healthWatchCalls.Load() != 0 {
		t.Fatal("metrics failed or eagerly set up health")
	}
	result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
	first := result.HealthReport
	if result.HealthError != nil || first == nil {
		t.Fatalf("health collection = %+v, %v", first, result.HealthError)
	}
	if first.GetRequestedSystems() != dcgmhealth.SystemMemory|dcgmhealth.SystemPCIe ||
		first.GetGroupId() != raw.GetGroupId() || first.GetNativeGroupOverallHealth() != 987 ||
		!proto.Equal(first.GetEntities()[0], raw.GetEntities()[0]) || !proto.Equal(first.GetIncidents()[0], raw.GetIncidents()[0]) ||
		!first.GetMonitoredSince().AsTime().Equal(monitoredSince) ||
		!proto.Equal(first.GetSystemErrors()[dcgmhealth.SystemPCIe], raw.GetSystemErrors()[dcgmhealth.SystemPCIe]) || !first.GetPossiblyTruncated() {
		t.Fatalf("raw report information changed: %v", first)
	}
	if raw.GetCollectionId() != "" || !raw.GetMonitoredSince().AsTime().Equal(monitoredSince) || raw.GetRequestedSystems() != 999 {
		t.Fatal("Source mutated native-owned report")
	}
	result = source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
	second := result.HealthReport
	if result.HealthError != nil || first.GetCollectionId() != cycle.CollectionID() || !proto.Equal(first, second) {
		t.Fatalf("second read = %+v, %v", second, result.HealthError)
	}
	nextCycle := newTestCycle(t, "next-cycle")
	result = source.Collect(context.Background(), nextCycle, CollectionRequest{Health: true})
	third := result.HealthReport
	if result.HealthError != nil || third.GetCollectionId() != nextCycle.CollectionID() || !third.GetMonitoredSince().AsTime().Equal(monitoredSince) {
		t.Fatalf("later read = %+v, %v", third, result.HealthError)
	}
	if native.initCalls.Load() != 1 || native.watchCalls.Load() != 1 || native.healthWatchCalls.Load() != 1 || native.health.readCalls.Load() != 3 || native.health.outsideGate.Load() {
		t.Fatal("metric and health did not share one initialized, gated lifetime")
	}
	first.Incidents[0].Message = "consumer edit"
	first.GetEntities()[0].GetNativeEntity().EntityId = 42
	*first.GroupId = 45
	first.MonitoredSince.Seconds++
	first.SystemErrors[dcgmhealth.SystemPCIe].Detail = "consumer edit"
	delete(first.SystemErrors, dcgmhealth.SystemPCIe)
	first.PossiblyTruncated = false
	if second.GetIncidents()[0].GetMessage() != "native detail" || second.GetEntities()[0].GetNativeEntity().GetEntityId() != 17 ||
		second.GetGroupId() != 23 || !second.GetMonitoredSince().AsTime().Equal(monitoredSince) ||
		second.GetSystemErrors()[dcgmhealth.SystemPCIe].GetDetail() != "PCIe discovery failed" || !second.GetPossiblyTruncated() {
		t.Fatal("reports from distinct reads alias each other")
	}
	if raw.GetIncidents()[0].GetMessage() != "native detail" || raw.GetEntities()[0].GetNativeEntity().GetEntityId() != 17 || raw.GetGroupId() != 23 ||
		!raw.GetMonitoredSince().AsTime().Equal(monitoredSince) || raw.GetSystemErrors()[dcgmhealth.SystemPCIe].GetDetail() != "PCIe discovery failed" || !raw.GetPossiblyTruncated() {
		t.Fatal("returned report aliases native-owned data")
	}
}

func TestHealthReportPreservesNativeAvailability(t *testing.T) {
	switchTarget := &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3}}}}
	unknownTarget := &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 71}}}
	for _, test := range []struct {
		failures map[uint64]*observation.CollectionError
		name     string
		entities []*observation.ObservationTarget
		missing  uint64
	}{
		{name: "empty", missing: dcgmhealth.KnownSystems},
		{name: "GPUs", entities: []*observation.ObservationTarget{healthTestGPUTarget(0)}, missing: dcgmhealth.NVSwitchSystems},
		{name: "switches", entities: []*observation.ObservationTarget{switchTarget}, missing: dcgmhealth.GPUSystems},
		{name: "both", entities: []*observation.ObservationTarget{healthTestGPUTarget(0), switchTarget}},
		{name: "unknown", entities: []*observation.ObservationTarget{unknownTarget}, missing: dcgmhealth.KnownSystems},
		{name: "GPU without native metadata", entities: []*observation.ObservationTarget{observation.GPUTarget("GPU-known")}, missing: dcgmhealth.NVSwitchSystems},
		{name: "raw known kind", entities: []*observation.ObservationTarget{{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 1}}}}, missing: dcgmhealth.KnownSystems},
		{name: "switch discovery failure", entities: []*observation.ObservationTarget{healthTestGPUTarget(0)}, failures: map[uint64]*observation.CollectionError{
			dcgmhealth.SystemNVSwitchFatal:    {Source: Name, Category: observation.CollectionErrorCategoryUnavailable, Detail: "switch discovery failed"},
			dcgmhealth.SystemNVSwitchNonfatal: {Source: Name, Category: observation.CollectionErrorCategoryUnavailable, Detail: "switch discovery failed"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := &observation.DCGMHealthReport{
				Entities: test.entities, SystemsWithoutTargets: test.missing, SystemErrors: test.failures,
				Incidents: []*observation.DCGMHealthIncident{{
					System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(),
					Entity: healthTestGPUTarget(99),
				}},
			}
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: raw}}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.KnownSystems); err != nil {
				t.Fatal(err)
			}
			report, failure := source.newCollectionReader(newTestCycle(t, "targets-"+test.name)).readHealth(context.Background())
			if failure != nil || report.GetSystemsWithoutTargets() != test.missing || len(report.GetIncidents()) != 1 ||
				!proto.Equal(report.GetMonitoredSince(), raw.GetMonitoredSince()) || len(report.GetSystemErrors()) != len(test.failures) {
				t.Fatalf("health collection = %v, %v; want missing %#x with native incident", report, failure, test.missing)
			}
			for system, want := range test.failures {
				if !proto.Equal(report.GetSystemErrors()[system], want) {
					t.Fatalf("native discovery error for %#x changed: %v", system, report.GetSystemErrors()[system])
				}
			}
			if raw.SystemsWithoutTargets != test.missing {
				t.Fatal("availability metadata mutated the native-owned report")
			}
		})
	}
}

func TestHealthWatchConstructionFailureRetriesAndKeepsMetrics(t *testing.T) {
	setupError := &dcgmError{category: observation.CollectionErrorCategoryNotConfigured, sourceError: "NATIVE_STATUS", detail: "health watch construction failed"}
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "native error", true: "missing watch"}[missing], func(t *testing.T) {
			gate := &sync.Mutex{}
			native := &healthLifecycleNative{
				healthSetupError: setupError, health: &fakeHealthWatch{},
				cleanupNative: cleanupNative{nativeCallGate: gate}, missingWatch: missing,
			}
			source := newSource(native, gate)
			t.Cleanup(func() { _ = source.Close() })
			registerMetricAndHealth(t, source)
			cycle := newTestCycle(t, "failed-health-construction")
			for attempt := range 2 {
				report, failure := source.newCollectionReader(cycle).readHealth(context.Background())
				if report != nil || failure == nil || failure.GetSource() != Name || failure.GetOperation() != "health" {
					t.Fatal("health setup unexpectedly succeeded")
				}
				if !missing && (failure.GetDetail() != setupError.Error() || failure.GetSourceError() != "NATIVE_STATUS" || failure.GetCategory() != observation.CollectionErrorCategoryNotConfigured) {
					t.Fatalf("health failure lost native status: %v", failure)
				}
				if missing && (failure.GetCategory() != observation.CollectionErrorCategorySourceError || !strings.Contains(failure.GetDetail(), "no watch")) {
					t.Fatalf("missing watch lost its contract failure: %v", failure)
				}
				if native.healthWatchCalls.Load() != int32(attempt+1) || native.health.readCalls.Load() != 0 || native.health.cleanupCalls.Load() != 0 {
					t.Fatal("failed construction retried inside one call or acquired native resources")
				}
			}
			values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
			if values[signalPowerDraw].Value == nil || source.closed.Load() || native.cleanupCalls.Load() != 0 {
				t.Fatalf("health construction failure blocked metrics or released the client: %v", values)
			}
			native.healthSetupError = nil
			native.missingWatch = false
			if report, failure := source.newCollectionReader(newTestCycle(t, "health-construction-recovered")).readHealth(context.Background()); failure != nil || report == nil {
				t.Fatalf("later health construction did not recover: %v, %v", report, failure)
			}
			if native.healthWatchCalls.Load() != 3 || native.health.readCalls.Load() != 1 || native.initCalls.Load() != 1 {
				t.Fatal("health construction recovery did not retain the shared client")
			}
			for range 2 {
				if err := source.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if native.health.cleanupCalls.Load() != 1 || native.watchCleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 || native.health.cleanedAfterClient.Load() || native.health.outsideGate.Load() || native.cleanupOutsideGate.Load() {
				t.Fatal("recovered watch or client cleanup repeated or escaped native-call ownership")
			}
		})
	}
}

func TestMetricSetupFailureIsTerminalAndKeepsHealth(t *testing.T) {
	setupError, rollbackError := errors.New("metric setup failed"), errors.New("metric rollback failed")
	gate := &sync.Mutex{}
	native := &healthLifecycleNative{cleanupNative: cleanupNative{setupError: setupError, cleanupError: rollbackError, nativeCallGate: gate}}
	source := newSource(native, gate)
	registerMetricAndHealth(t, source)
	for _, cycleID := range []string{"failed-metric", "metric-retry"} {
		values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, cycleID), 0, []string{signalPowerDraw})
		failure := values[signalPowerDraw].CollectionError
		if !strings.Contains(failure.GetDetail(), setupError.Error()) || !strings.Contains(failure.GetDetail(), rollbackError.Error()) {
			t.Fatalf("metric error lost native rollback details: %v", failure)
		}
	}
	if source.closed.Load() || native.watchCalls.Load() != 1 || native.cleanupCalls.Load() != 0 ||
		source.session == nil || !errors.Is(source.session.metricWatchError, setupError) || !errors.Is(source.session.metricWatchError, rollbackError) {
		t.Fatal("combined source retried metric setup or released client")
	}
	if failure := source.Collect(context.Background(), newTestCycle(t, "health-after-metric-failure"), CollectionRequest{Health: true}).HealthError; failure != nil {
		t.Fatal(failure)
	}
	for range 2 {
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if native.watchCleanupCalls.Load() != 1 || native.health.cleanupCalls.Load() != 1 || native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
		t.Fatal("combined source repeated failed rollback or lost successful health cleanup")
	}
}

type healthPreflightNative struct {
	failure error
	healthLifecycleNative
	preparationCalls atomic.Int32
}

func (native *healthPreflightNative) MetricWatchGroups([]string) ([]metricWatchGroup, error) {
	native.preparationCalls.Add(1)
	return nil, native.failure
}

func TestCombinedMetricPreflightFailureKeepsHealth(t *testing.T) {
	native := &healthPreflightNative{failure: errors.New("metric configuration rejected")}
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	if failure := source.Collect(context.Background(), newTestCycle(t, "health-after-preflight"), CollectionRequest{Health: true}).HealthError; failure != nil {
		t.Fatal(failure)
	}
	if native.preparationCalls.Load() != 0 {
		t.Fatal("health collection prepared metric watches")
	}
	for _, cycleID := range []string{"failed-preflight", "preflight-retry"} {
		values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, cycleID), 0, []string{signalPowerDraw})
		if failure := values[signalPowerDraw].CollectionError; failure.GetDetail() != native.failure.Error() {
			t.Fatalf("metric error = %v", failure)
		}
	}
	if native.preparationCalls.Load() != 1 || native.watchCalls.Load() != 0 || native.initCalls.Load() != 1 {
		t.Fatal("combined metric preflight failure entered or retried metric setup")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHealthReadFailureCanRetryAndKeepsMetrics(t *testing.T) {
	for _, outcome := range []string{"native error", "setup and rollback error", "missing report"} {
		t.Run(outcome, func(t *testing.T) {
			watch := &fakeHealthWatch{missingReport: outcome == "missing report"}
			wantDetail := "no report"
			if outcome == "native error" {
				watch.readError = errors.New("health query failed")
				wantDetail = watch.readError.Error()
			} else if outcome == "setup and rollback error" {
				watch.readError = errors.Join(
					&dcgmError{category: observation.CollectionErrorCategoryUnavailable, sourceError: "NATIVE_STATUS", detail: "health setup failed"},
					errors.New("health rollback failed"),
				)
				wantDetail = watch.readError.Error()
			}
			native := &healthLifecycleNative{health: watch}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			registerMetricAndHealth(t, source)
			cycle := newTestCycle(t, "query-failure")
			report, failure := source.newCollectionReader(cycle).readHealth(context.Background())
			if report != nil || failure.GetOperation() != "health" || failure.GetSource() != Name || !strings.Contains(failure.GetDetail(), wantDetail) {
				t.Fatalf("query failure = %v", failure)
			}
			if outcome == "missing report" && failure.GetCategory() != observation.CollectionErrorCategorySourceError {
				t.Fatalf("missing report category = %v", failure)
			}
			if outcome == "setup and rollback error" && (failure.GetSourceError() != "NATIVE_STATUS" || failure.GetCategory() != observation.CollectionErrorCategoryUnavailable) {
				t.Fatalf("setup and rollback failure lost native status: %v", failure)
			}
			values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
			if values[signalPowerDraw].Value == nil {
				t.Fatal("completed health query failure blocked metrics in the same cycle")
			}
			native.health.readError = nil
			native.health.missingReport = false
			recovered, failure := source.newCollectionReader(cycle).readHealth(context.Background())
			if failure != nil || recovered == nil || len(recovered.GetIncidents()) != 0 {
				t.Fatalf("recovered empty health report = %v, %v", recovered, failure)
			}
			if native.healthWatchCalls.Load() != 1 || native.health.readCalls.Load() != 2 || native.health.cleanupCalls.Load() != 0 || native.cleanupCalls.Load() != 0 {
				t.Fatal("health read error discarded the watch that owns lazy setup and cleanup")
			}
		})
	}
}

func TestCloseAttemptsBothFeatureCleanupsBeforeClient(t *testing.T) {
	metricError, healthError := errors.New("metric cleanup failed"), errors.New("health cleanup failed")
	gate := &sync.Mutex{}
	native := &healthLifecycleNative{
		health:        &fakeHealthWatch{cleanupError: healthError},
		cleanupNative: cleanupNative{cleanupError: metricError, nativeCallGate: gate},
	}
	source := newSource(native, gate)
	registerMetricAndHealth(t, source)
	cycle := newTestCycle(t, "both-features")
	if values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw}); values[signalPowerDraw].Value == nil {
		t.Fatal(values)
	}
	if failure := source.Collect(context.Background(), cycle, CollectionRequest{Health: true}).HealthError; failure != nil {
		t.Fatal(failure)
	}
	if err := source.Close(); !errors.Is(err, metricError) || !errors.Is(err, healthError) {
		t.Fatalf("Close() lost cleanup errors: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("second Close() repeated cleanup: %v", err)
	}
	if native.watchCleanupCalls.Load() != 1 || native.health.cleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 || native.health.cleanedAfterClient.Load() || native.watchCleanedAfterClient.Load() || native.health.outsideGate.Load() || native.cleanupOutsideGate.Load() {
		t.Fatal("cleanup did not consume each feature once before releasing the client under the gate")
	}
	if err := source.AddHealthToWatch(dcgmhealth.SystemPCIe); err == nil {
		t.Fatal("closed source accepted health registration")
	}
	if failure := source.Collect(context.Background(), newTestCycle(t, "closed-health"), CollectionRequest{Health: true}).HealthError; failure.GetCategory() != observation.CollectionErrorCategoryUnavailable || native.health.readCalls.Load() != 1 {
		t.Fatalf("health read after Close = %v", failure)
	}
}

func TestHealthUUIDEnrichmentPreservesNativeIdentity(t *testing.T) {
	report := &observation.DCGMHealthReport{
		Entities: []*observation.ObservationTarget{healthTestGPUTarget(2), {Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3, EntityId: 7}}}}},
		Incidents: []*observation.DCGMHealthIncident{
			{Entity: healthTestGPUTarget(9)},
			{Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 71, EntityId: 9}}}},
		},
	}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: report}}
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	result, failure := source.newCollectionReader(newTestCycle(t, "enriched-health")).readHealth(context.Background())
	if failure != nil {
		t.Fatal(failure)
	}
	if result.Entities[0].GetGpu().GetUuid() != "GPU-2" || result.Incidents[0].Entity.GetGpu().GetUuid() != "GPU-9" ||
		result.Entities[1].GetGpu().GetUuid() != "" || result.Incidents[1].Entity.GetGpu().GetUuid() != "" {
		t.Fatalf("UUID enrichment rewrote native identities: %v", result)
	}
	if report.Entities[0].GetGpu().GetUuid() != "" || report.Incidents[0].Entity.GetGpu().GetUuid() != "" || native.uuidCalls.Load() != 1 {
		t.Fatal("enrichment mutated native report or queried UUIDs repeatedly")
	}
	if result.Entities[0].GetGpu().GetSourceLocalId() != "" ||
		!proto.Equal(result.Entities[0].GetGpu().GetNative(), report.Entities[0].GetGpu().GetNative()) ||
		!proto.Equal(result.Entities[1], report.Entities[1]) || !proto.Equal(result.Incidents[1].Entity, report.Incidents[1].Entity) {
		t.Fatal("UUID enrichment added redundant identity metadata or changed native identity")
	}
	result.Entities[0].GetGpu().Native.EntityId = 33
	if report.Entities[0].GetGpu().GetNative().GetEntityId() != 2 {
		t.Fatal("returned report aliases native identity metadata")
	}
}

func TestHealthUUIDEnrichmentRequiresTypedDCGMGPUIdentity(t *testing.T) {
	for _, test := range []struct {
		target *observation.ObservationTarget
		name   string
	}{
		{name: "UUID only", target: observation.GPUTarget("GPU-known")},
		{name: "other source", target: &observation.ObservationTarget{Kind: &observation.ObservationTarget_GPU{Gpu: &observation.GPURef{Native: &observation.NativeEntityRef{Source: "other", EntityKind: 1, EntityId: 2}}}}},
		{name: "other kind", target: &observation.ObservationTarget{Kind: &observation.ObservationTarget_GPU{Gpu: &observation.GPURef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 71, EntityId: 2}}}}},
		{name: "raw GPU kind", target: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 1, EntityId: 2}}}},
		{name: "switch", target: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3, EntityId: 2}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: &observation.DCGMHealthReport{Entities: []*observation.ObservationTarget{test.target}}}}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.KnownSystems); err != nil {
				t.Fatal(err)
			}
			report, failure := source.newCollectionReader(newTestCycle(t, "skip-uuid-"+test.name)).readHealth(context.Background())
			if failure != nil || native.uuidCalls.Load() != 0 || !proto.Equal(report.Entities[0], test.target) {
				t.Fatalf("health collection = %v, %v; UUID calls = %d", report, failure, native.uuidCalls.Load())
			}
		})
	}
}

func TestHealthUUIDCancellationPreservesIncidentsAndNativeGate(t *testing.T) {
	gate := &sync.Mutex{}
	native := &healthLifecycleNative{
		health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
			Entities: []*observation.ObservationTarget{healthTestGPUTarget(2)},
			Incidents: []*observation.DCGMHealthIncident{{
				System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(), ErrorCode: 123, Message: "memory incident",
				Entity: healthTestGPUTarget(2),
			}},
		}},
	}
	native.uuidStarted, native.releaseRead = make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(native.releaseRead) })
	t.Cleanup(release)
	source := newSource(native, gate)
	registerMetricAndHealth(t, source)
	cycle := newTestCycle(t, "health-uuid-cancellation")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var collected CollectionResult
	finished := make(chan struct{})
	go func() {
		collected = source.Collect(ctx, cycle, CollectionRequest{Health: true})
		close(finished)
	}()
	select {
	case <-native.uuidStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("native UUID enrichment did not start")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("health read did not return after UUID enrichment cancellation")
	}
	result, failure := collected.HealthReport, collected.HealthError
	if failure != nil || len(result.GetIncidents()) != 1 || result.Incidents[0].GetMessage() != "memory incident" ||
		result.Incidents[0].Entity.GetGpu().GetNative().GetEntityId() != 2 || result.Incidents[0].Entity.GetGpu().GetUuid() != "" {
		t.Fatalf("optional cancellation lost native report: %v, %v", result, failure)
	}
	if native.uuidCalls.Load() != 1 || native.health.readCalls.Load() != 1 {
		t.Fatal("test did not reach optional UUID enrichment")
	}
	for _, laterCycle := range []*collect.Cycle{cycle, newTestCycle(t, "after-uuid-cancellation")} {
		values := source.ReadGPUMetrics(context.Background(), laterCycle, 2, []string{signalPowerDraw})
		if values[signalPowerDraw].CollectionError == nil || native.readCalls.Load() != 0 {
			t.Fatal("metric read entered DCGM during unfinished UUID enrichment")
		}
	}
	if err := source.Close(); !errors.Is(err, ErrInUse) || native.cleanupCalls.Load() != 0 {
		t.Fatalf("Close released resources during UUID enrichment: %v", err)
	}
	release()
	waitForNativeGate(t, gate)
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if native.health.cleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
		t.Fatal("Close lost health or client cleanup after enrichment completed")
	}
}

func TestHealthCancellationSuppressesSharedSourceAndPreservesCleanup(t *testing.T) {
	gate := &sync.Mutex{}
	watch := &fakeHealthWatch{readStarted: make(chan struct{}), releaseRead: make(chan struct{})}
	native := &healthLifecycleNative{health: watch, cleanupNative: cleanupNative{nativeCallGate: gate}}
	source := newSource(native, gate)
	registerMetricAndHealth(t, source)
	release := sync.OnceFunc(func() { close(watch.releaseRead) })
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cycle := newTestCycle(t, "interrupted-health")
	finished := make(chan *observation.CollectionError, 1)
	go func() {
		finished <- source.Collect(ctx, cycle, CollectionRequest{Health: true}).HealthError
	}()
	select {
	case <-watch.readStarted:
	case <-time.After(time.Second):
		t.Fatal("native health read did not start")
	}
	cancel()
	select {
	case failure := <-finished:
		if failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Fatalf("canceled health read = %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("health cancellation did not return")
	}
	for _, laterCycle := range []*collect.Cycle{cycle, newTestCycle(t, "later-cycle")} {
		values := source.ReadGPUMetrics(context.Background(), laterCycle, 0, []string{signalPowerDraw})
		if values[signalPowerDraw].CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable || native.watchCalls.Load() != 0 {
			t.Fatal("metric setup entered during unfinished native health read")
		}
		if failure := source.Collect(context.Background(), laterCycle, CollectionRequest{Health: true}).HealthError; failure.GetCategory() != observation.CollectionErrorCategoryUnavailable || watch.readCalls.Load() != 1 {
			t.Fatal("health call re-entered during unfinished native health read")
		}
	}
	if err := source.Close(); !errors.Is(err, ErrInUse) || native.cleanupCalls.Load() != 0 || watch.cleanupCalls.Load() != 0 {
		t.Fatalf("busy Close() released active resources: %v", err)
	}
	release()
	waitForNativeGate(t, gate)
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if watch.cleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
		t.Fatal("busy Close lost health or client cleanup")
	}
}

func TestNativeTimeoutSuppressesBothFeaturesForCycle(t *testing.T) {
	for _, feature := range []string{"metric", "health"} {
		t.Run(feature, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gate := &sync.Mutex{}
				started, released := make(chan struct{}), make(chan struct{})
				native := &healthLifecycleNative{health: &fakeHealthWatch{}, cleanupNative: cleanupNative{nativeCallGate: gate}}
				if feature == "metric" {
					native.fakeNative.readStarted = started
					native.fakeNative.releaseRead = released
				} else {
					native.health.readStarted = started
					native.health.releaseRead = released
				}
				release := sync.OnceFunc(func() { close(released) })
				defer release()
				source := newSource(native, gate)
				registerMetricAndHealth(t, source)
				const timeout = 100 * time.Millisecond
				cycle, err := collect.NewCycleWithTimeout("native-timeout-"+feature, time.Now(), timeout)
				if err != nil {
					t.Fatal(err)
				}
				finished := make(chan *observation.CollectionError, 1)
				go func() {
					if feature == "metric" {
						finished <- source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})[signalPowerDraw].CollectionError
					} else {
						finished <- source.Collect(context.Background(), cycle, CollectionRequest{Health: true}).HealthError
					}
				}()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("native read did not start")
				}
				synctest.Wait()
				time.Sleep(timeout)
				select {
				case failure := <-finished:
					if failure.GetCategory() != observation.CollectionErrorCategoryTimeout {
						t.Fatalf("native timeout = %v", failure)
					}
				case <-time.After(time.Second):
					t.Fatal("native read did not time out")
				}
				for _, nextCycle := range []*collect.Cycle{cycle, newTestCycle(t, "next-cycle")} {
					wantCategory := observation.CollectionErrorCategoryUnavailable
					if nextCycle == cycle {
						wantCategory = observation.CollectionErrorCategoryTimeout
					}
					values := source.ReadGPUMetrics(context.Background(), nextCycle, 0, []string{signalPowerDraw})
					if values[signalPowerDraw].CollectionError.GetCategory() != wantCategory {
						t.Fatalf("metric read after %s timeout = %v", feature, values)
					}
					if failure := source.Collect(context.Background(), nextCycle, CollectionRequest{Health: true}).HealthError; failure.GetCategory() != wantCategory {
						t.Fatalf("health read after %s timeout = %v", feature, failure)
					}
				}
				if native.readCalls.Load()+native.health.readCalls.Load() != 1 {
					t.Fatal("timed-out native call allowed another feature to enter")
				}
				release()
				synctest.Wait()
				waitForNativeGate(t, gate)
				if failure := source.Collect(context.Background(), cycle, CollectionRequest{Health: true}).HealthError; failure.GetCategory() != observation.CollectionErrorCategoryTimeout {
					t.Fatal("completed native call cleared the cycle's timeout suppression")
				}
				if err := source.Close(); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

type healthBlockingSetupNative struct {
	started chan struct{}
	release chan struct{}
	healthLifecycleNative
	blockWatch bool
}

func (native *healthBlockingSetupNative) Init() (func() error, error) {
	if !native.blockWatch {
		close(native.started)
		<-native.release
	}
	return native.healthLifecycleNative.Init()
}

func (native *healthBlockingSetupNative) WatchHealth(systems uint64) (healthWatch, error) {
	if native.blockWatch {
		close(native.started)
		<-native.release
	}
	return native.healthLifecycleNative.WatchHealth(systems)
}

func TestCloseDuringHealthSetupPreventsNativeRead(t *testing.T) {
	for _, stage := range []string{"init", "watch"} {
		t.Run(stage, func(t *testing.T) {
			native := &healthBlockingSetupNative{started: make(chan struct{}), release: make(chan struct{}), blockWatch: stage == "watch"}
			source := newSource(native, &sync.Mutex{})
			registerMetricAndHealth(t, source)
			release := sync.OnceFunc(func() { close(native.release) })
			t.Cleanup(release)
			finished := make(chan *observation.CollectionError, 1)
			cycle := newTestCycle(t, "close-during-health-"+stage)
			go func() {
				finished <- source.Collect(context.Background(), cycle, CollectionRequest{Health: true}).HealthError
			}()
			select {
			case <-native.started:
			case <-time.After(time.Second):
				t.Fatal("health setup did not start")
			}
			if err := source.Close(); !errors.Is(err, ErrInUse) {
				t.Fatalf("busy Close = %v", err)
			}
			release()
			select {
			case failure := <-finished:
				if failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatalf("health setup continued after Close: %v", failure)
				}
			case <-time.After(time.Second):
				t.Fatal("health setup did not finish")
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			wantWatches := int32(0)
			if stage == "watch" {
				wantWatches = 1
				if native.health.readCalls.Load() != 0 || native.health.cleanupCalls.Load() != 1 {
					t.Fatal("Close lost health watch or allowed read after setup")
				}
			}
			if native.healthWatchCalls.Load() != wantWatches || native.cleanupCalls.Load() != 1 {
				t.Fatal("Close during setup did not release exactly one client reference")
			}
		})
	}
}

func waitForNativeGate(t *testing.T, gate *sync.Mutex) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if gate.TryLock() {
			gate.Unlock()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("native operation did not release gate")
}
