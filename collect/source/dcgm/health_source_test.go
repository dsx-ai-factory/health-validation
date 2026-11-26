// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func (watch *fakeHealthWatch) Read() (*healthGroupRead, error) {
	watch.recordGate()
	watch.readCalls.Add(1)
	if watch.readStarted != nil {
		close(watch.readStarted)
		<-watch.releaseRead
	}
	if watch.report == nil {
		if watch.readError != nil || watch.missingReport {
			return nil, watch.readError
		}
		return &healthGroupRead{group: &observation.DCGMHealthGroupResult{}}, nil
	}
	group := &observation.DCGMHealthGroupResult{}
	if len(watch.report.GroupResults) != 0 {
		group = watch.report.GroupResults[0]
	}
	return &healthGroupRead{
		group: group, systemErrors: watch.report.SystemErrors,
		systemsWithoutTargets: watch.report.SystemsWithoutTargets,
	}, watch.readError
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
	healthSetupError  error
	healthSetupErrors map[uint64]error
	gpuHealth         map[uint]*fakeHealthWatch
	gpuSetupErrors    map[uint]error
	health            *fakeHealthWatch
	switchHealth      *fakeHealthWatch
	healthMasks       []uint64
	cleanupNative
	healthSystems    uint64
	healthWatchCalls atomic.Int32
	missingWatch     bool
}

func (native *healthLifecycleNative) WatchHealth(systems uint64, gpuID *uint) (healthWatch, error) {
	native.healthWatchCalls.Add(1)
	native.healthSystems |= systems
	native.healthMasks = append(native.healthMasks, systems)
	if native.missingWatch {
		return nil, nil
	}
	if native.healthSetupError != nil {
		return nil, native.healthSetupError
	}
	if err := native.healthSetupErrors[systems]; err != nil {
		return nil, err
	}
	if gpuID == nil {
		if native.switchHealth == nil {
			native.switchHealth = &fakeHealthWatch{report: &observation.DCGMHealthReport{SystemsWithoutTargets: systems}}
		}
		native.switchHealth.native = &native.fakeNative
		native.switchHealth.gate = native.nativeCallGate
		return native.switchHealth, nil
	}
	if err := native.gpuSetupErrors[*gpuID]; err != nil {
		return nil, err
	}
	watch := native.health
	if native.gpuHealth != nil {
		watch = native.gpuHealth[*gpuID]
	}
	if watch == nil {
		watch = &fakeHealthWatch{}
		if native.gpuHealth != nil {
			native.gpuHealth[*gpuID] = watch
		} else {
			native.health = watch
		}
	}
	watch.native = &native.fakeNative
	watch.gate = native.nativeCallGate
	return watch, nil
}

func (native *healthLifecycleNative) HealthGPUIndices() ([]uint, error) {
	if native.healthIndices != nil || native.healthEnumerationErr != nil {
		return native.fakeNative.HealthGPUIndices()
	}
	native.healthEnumerationCalls.Add(1)
	if native.gpuHealth != nil {
		ids := make([]uint, 0, len(native.gpuHealth))
		for id := range native.gpuHealth {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return ids, nil
	}
	var report *observation.DCGMHealthReport
	if native.health != nil {
		report = native.health.report
	}
	for _, group := range report.GetGroupResults() {
		for _, entry := range group.EntityResults {
			gpu := entry.GetEntity().GetGpu()
			if gpu.GetNative().GetSource() == Name && gpu.GetNative().GetEntityKind() == gpuEntityKind {
				return []uint{uint(gpu.GetNative().GetEntityId())}, nil
			}
		}
	}
	return []uint{0}, nil
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
		GroupResults: []*observation.DCGMHealthGroupResult{{
			NativeOverallHealth: observation.DCGMHealthResultPass.Enum(), MonitoredSince: monitoredSince,
			EntityResults: []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)}},
		}},
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
		if failure != nil || report == nil || len(report.GetGroupResults()[0].GetEntityResults()) != 1 || len(report.GroupResults[0].EntityResults[0].GetIncidents()) != 0 {
			t.Fatalf("empty health report = %v, %v", report, failure)
		}
		gpu := report.GroupResults[0].EntityResults[0].GetEntity().GetGpu()
		if len(report.GetGroupResults()) != 2 || report.GroupResults[0].GetNativeOverallHealth() != observation.DCGMHealthResultPass ||
			report.GetRequestedSystems() != systems || report.GetSystemsWithoutTargets() != dcgmhealth.SystemNVSwitchFatal ||
			gpu.GetUuid() != "GPU-2" || gpu.GetNative().GetEntityId() != 2 || !report.GroupResults[0].EntityResults[0].GetGroupMember() ||
			report.GetCollectionId() != cycle.CollectionID() || !proto.Equal(report.GroupResults[0].GetMonitoredSince(), monitoredSince) {
			t.Fatalf("empty health report lost metadata: %v", report)
		}
	}
	if native.healthWatchCalls.Load() != 2 || native.health.readCalls.Load() != 2 || native.switchHealth.readCalls.Load() != 2 || native.initCalls.Load() != 1 {
		t.Fatal("repeated empty reads did not retain both health watches")
	}
	if !proto.Equal(raw.GroupResults[0].GetMonitoredSince(), monitoredSince) || raw.GetCollectionId() != "" || raw.GroupResults[0].EntityResults[0].GetEntity().GetGpu().GetUuid() != "" {
		t.Fatal("empty health read mutated the native-owned report")
	}
}

func TestHealthReportMetadataAndSharedClient(t *testing.T) {
	gate := &sync.Mutex{}
	monitoredSince := time.Unix(100, 0)
	raw := &observation.DCGMHealthReport{
		RequestedSystems: 999,
		GroupResults: []*observation.DCGMHealthGroupResult{{
			NativeOverallHealth: observation.DCGMHealthResult(987).Enum(), MonitoredSince: timestamppb.New(monitoredSince), PossiblyTruncated: true,
			EntityResults: []*observation.DCGMHealthEntityResult{
				{
					Entity:      &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 51, EntityId: 17}}},
					GroupMember: proto.Bool(true),
					Incidents:   []*observation.DCGMHealthIncident{{System: 9999, NativeHealth: observation.DCGMHealthResult(777).Enum(), ErrorCode: 1234, Message: "native detail"}},
				},
				{
					Entity:           &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 51, EntityId: 18}}},
					GroupMember:      proto.Bool(false),
					CollectionErrors: []*observation.DCGMHealthEntityError{{Systems: dcgmhealth.SystemMemory, Error: &observation.CollectionError{Source: Name, Operation: "health membership", Detail: "member rejected"}}},
				},
			},
		}},
		SystemErrors: map[uint64]*observation.CollectionError{
			dcgmhealth.SystemPCIe: {Source: Name, Operation: "health discovery", Category: observation.CollectionErrorCategoryUnavailable, Detail: "PCIe discovery failed"},
		},
	}
	original := proto.Clone(raw).(*observation.DCGMHealthReport)
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
	expected := proto.Clone(raw).(*observation.DCGMHealthReport)
	expected.RequestedSystems = dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe
	expected.CollectionId = cycle.CollectionID()
	expected.GroupResults[0].EntityKind = gpuEntityKind
	expected.GroupResults[0].Systems = dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe
	if result.HealthError != nil || !proto.Equal(first, expected) {
		t.Fatalf("raw report information changed: %v, %v", first, result.HealthError)
	}
	if !proto.Equal(raw, original) {
		t.Fatal("Source mutated native-owned report")
	}
	result = source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
	second := result.HealthReport
	if result.HealthError != nil || !proto.Equal(second, expected) {
		t.Fatalf("second read = %+v, %v", second, result.HealthError)
	}
	nextCycle := newTestCycle(t, "next-cycle")
	result = source.Collect(context.Background(), nextCycle, CollectionRequest{Health: true})
	third := result.HealthReport
	if result.HealthError != nil || third.GetCollectionId() != nextCycle.CollectionID() || !third.GroupResults[0].GetMonitoredSince().AsTime().Equal(monitoredSince) {
		t.Fatalf("later read = %+v, %v", third, result.HealthError)
	}
	if native.initCalls.Load() != 1 || native.watchCalls.Load() != 1 || native.healthWatchCalls.Load() != 1 || native.health.readCalls.Load() != 3 || native.health.outsideGate.Load() {
		t.Fatal("metric and health did not share one initialized, gated lifetime")
	}
	first.GroupResults[0].EntityResults[0].Incidents[0].Message = "consumer edit"
	first.GroupResults[0].EntityResults[0].Entity.GetNativeEntity().EntityId = 42
	*first.GroupResults[0].EntityResults[0].GroupMember = false
	*first.GroupResults[0].NativeOverallHealth = observation.DCGMHealthResultPass
	first.GroupResults[0].MonitoredSince.Seconds++
	first.SystemErrors[dcgmhealth.SystemPCIe].Detail = "consumer edit"
	delete(first.SystemErrors, dcgmhealth.SystemPCIe)
	first.GroupResults[0].EntityResults[1].CollectionErrors[0].Error.Detail = "consumer edit"
	first.GroupResults[0].EntityResults[1].Entity.GetNativeEntity().EntityId = 43
	first.GroupResults[0].PossiblyTruncated = false
	if !proto.Equal(second, expected) {
		t.Fatal("reports from distinct reads alias each other")
	}
	if !proto.Equal(raw, original) {
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
			gpuReport := &observation.DCGMHealthReport{GroupResults: []*observation.DCGMHealthGroupResult{{}}, SystemsWithoutTargets: test.missing & dcgmhealth.GPUSystems}
			switchReport := &observation.DCGMHealthReport{GroupResults: []*observation.DCGMHealthGroupResult{{}}, SystemsWithoutTargets: test.missing & dcgmhealth.NVSwitchSystems, SystemErrors: test.failures}
			for _, entity := range test.entities {
				part := gpuReport
				if entity.GetNvSwitch() != nil {
					part = switchReport
				}
				part.GroupResults[0].EntityResults = append(part.GroupResults[0].EntityResults, &observation.DCGMHealthEntityResult{Entity: entity, GroupMember: proto.Bool(true)})
			}
			gpuReport.GroupResults[0].EntityResults = append(gpuReport.GroupResults[0].EntityResults, &observation.DCGMHealthEntityResult{
				Entity: healthTestGPUTarget(99), GroupMember: proto.Bool(false),
				Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum()}},
			})
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: gpuReport}, switchHealth: &fakeHealthWatch{report: switchReport}}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.KnownSystems); err != nil {
				t.Fatal(err)
			}
			report, failure := source.newCollectionReader(newTestCycle(t, "targets-"+test.name)).readHealth(context.Background())
			entityCount := 0
			for _, group := range report.GetGroupResults() {
				entityCount += len(group.GetEntityResults())
			}
			if failure != nil || report.GetSystemsWithoutTargets() != test.missing || entityCount != len(test.entities)+1 ||
				len(report.GetSystemErrors()) != len(test.failures) {
				t.Fatalf("health collection = %v, %v; want missing %#x with native incident", report, failure, test.missing)
			}
			var incidentOnly *observation.DCGMHealthEntityResult
			for _, group := range report.GroupResults {
				for _, entry := range group.EntityResults {
					if entry.Entity.GetGpu().GetNative().GetEntityId() == 99 {
						incidentOnly = entry
					}
				}
			}
			if incidentOnly.GetGroupMember() || len(incidentOnly.GetIncidents()) != 1 || incidentOnly.GetEntity().GetGpu().GetUuid() != "GPU-99" {
				t.Fatalf("incident-only entity changed: %v", incidentOnly)
			}
			for system, want := range test.failures {
				if !proto.Equal(report.GetSystemErrors()[system], want) {
					t.Fatalf("native discovery error for %#x changed: %v", system, report.GetSystemErrors()[system])
				}
			}
			if gpuReport.SystemsWithoutTargets != test.missing&dcgmhealth.GPUSystems || switchReport.SystemsWithoutTargets != test.missing&dcgmhealth.NVSwitchSystems {
				t.Fatal("availability metadata mutated the native-owned reports")
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
				if failure != nil || len(report.GetGroupResults()) != 1 || report.GroupResults[0].GetCollectionError() == nil ||
					len(report.GroupResults[0].EntityResults) != 1 || report.GroupResults[0].EntityResults[0].GroupMember != nil {
					t.Fatalf("health setup lost the discovered target: %v, %v", report, failure)
				}
				failure = report.GroupResults[0].CollectionError
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
			if failure != nil || len(report.GetGroupResults()) != 1 || report.GroupResults[0].GetCollectionError() == nil || len(report.GroupResults[0].EntityResults) != 1 || report.GroupResults[0].EntityResults[0].GroupMember != nil {
				t.Fatalf("failed read lost the discovered GPU: %v, %v", report, failure)
			}
			failure = report.GroupResults[0].CollectionError
			if failure.GetOperation() != "health" || failure.GetSource() != Name || !strings.Contains(failure.GetDetail(), wantDetail) {
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
			if failure != nil || recovered == nil || len(recovered.GetGroupResults()[0].GetEntityResults()) != 0 {
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
	switchError := errors.New("switch cleanup failed")
	gate := &sync.Mutex{}
	native := &healthLifecycleNative{
		health:        &fakeHealthWatch{cleanupError: healthError},
		switchHealth:  &fakeHealthWatch{cleanupError: switchError},
		cleanupNative: cleanupNative{cleanupError: metricError, nativeCallGate: gate},
	}
	source := newSource(native, gate)
	registerMetricAndHealth(t, source)
	if err := source.AddHealthToWatch(dcgmhealth.SystemNVSwitchFatal); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "both-features")
	if values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw}); values[signalPowerDraw].Value == nil {
		t.Fatal(values)
	}
	if failure := source.Collect(context.Background(), cycle, CollectionRequest{Health: true}).HealthError; failure != nil {
		t.Fatal(failure)
	}
	if err := source.Close(); !errors.Is(err, metricError) || !errors.Is(err, healthError) || !errors.Is(err, switchError) {
		t.Fatalf("Close() lost cleanup errors: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("second Close() repeated cleanup: %v", err)
	}
	if native.watchCleanupCalls.Load() != 1 || native.health.cleanupCalls.Load() != 1 || native.switchHealth.cleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 ||
		native.health.cleanedAfterClient.Load() || native.switchHealth.cleanedAfterClient.Load() || native.watchCleanedAfterClient.Load() ||
		native.health.outsideGate.Load() || native.switchHealth.outsideGate.Load() || native.cleanupOutsideGate.Load() {
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
		GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{
			{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)},
			{Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3, EntityId: 7}}}}, GroupMember: proto.Bool(true)},
			{Entity: healthTestGPUTarget(9), GroupMember: proto.Bool(false), Incidents: []*observation.DCGMHealthIncident{{Message: "incident only"}}},
			{Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 71, EntityId: 9}}}, GroupMember: proto.Bool(false), Incidents: []*observation.DCGMHealthIncident{{Message: "unknown entity"}}},
			{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)},
			{Entity: healthTestGPUTarget(3), GroupMember: proto.Bool(false), CollectionErrors: []*observation.DCGMHealthEntityError{{Systems: dcgmhealth.SystemMemory, Error: &observation.CollectionError{Detail: "membership failure"}}}},
		}}},
	}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: report}}
	native.uuidReadIndices = make(chan []uint, 1)
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	result, failure := source.newCollectionReader(newTestCycle(t, "enriched-health")).readHealth(context.Background())
	if failure != nil {
		t.Fatal(failure)
	}
	for index, want := range []string{"GPU-2", "", "GPU-9", "", "GPU-2", "GPU-3"} {
		entry := result.GroupResults[0].EntityResults[index]
		if entry.Entity.GetGpu().GetUuid() != want || !proto.Equal(entry.Entity.GetGpu().GetNative(), report.GroupResults[0].EntityResults[index].Entity.GetGpu().GetNative()) {
			t.Fatalf("UUID enrichment rewrote entity %d: %v", index, entry)
		}
		if report.GroupResults[0].EntityResults[index].Entity.GetGpu().GetUuid() != "" {
			t.Fatal("enrichment mutated the native-owned report")
		}
	}
	if native.uuidCalls.Load() != 1 {
		t.Fatal("enrichment queried UUIDs repeatedly")
	}
	if ids := <-native.uuidReadIndices; !slices.Equal(ids, []uint{2, 9, 3}) {
		t.Fatalf("identity lookup IDs = %v, want each report GPU once", ids)
	}
	if result.GroupResults[0].EntityResults[0].Entity.GetGpu().GetSourceLocalId() != "" ||
		!proto.Equal(result.GroupResults[0].EntityResults[1], report.GroupResults[0].EntityResults[1]) || !proto.Equal(result.GroupResults[0].EntityResults[3], report.GroupResults[0].EntityResults[3]) {
		t.Fatal("UUID enrichment added redundant identity metadata or changed native identity")
	}
	result.GroupResults[0].EntityResults[0].Entity.GetGpu().Native.EntityId = 33
	result.GroupResults[0].EntityResults[5].Entity.GetGpu().Native.EntityId = 34
	if report.GroupResults[0].EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != 2 || report.GroupResults[0].EntityResults[5].Entity.GetGpu().GetNative().GetEntityId() != 3 {
		t.Fatal("returned report aliases native identity metadata")
	}
}

func TestHealthMembershipErrorIdentityEnrichment(t *testing.T) {
	unavailable := &observation.CollectionError{Source: Name, Operation: "identity", Category: observation.CollectionErrorCategoryUnavailable}
	for _, test := range []struct {
		lookupError error
		name        string
		identity    GPUIdentityEntry
	}{
		{name: "UUID and PCI address", identity: GPUIdentityEntry{UUID: "GPU-3", BusID: "0000:53:00.0"}},
		{name: "UUID unavailable", identity: GPUIdentityEntry{UUIDError: unavailable, BusID: "0000:53:00.0"}},
		{name: "PCI address unavailable", identity: GPUIdentityEntry{UUID: "GPU-3", BusIDError: unavailable}},
		{name: "lookup failed", lookupError: errors.New("identity lookup failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := &observation.DCGMHealthReport{
				GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{{
					Entity: healthTestGPUTarget(3), GroupMember: proto.Bool(false),
					CollectionErrors: []*observation.DCGMHealthEntityError{{
						Systems: dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe,
						Error:   &observation.CollectionError{Source: Name, Operation: "health membership", SourceError: "DCGM_ST_GPU_IS_LOST", Detail: "GPU rejected"},
					}},
				}}}},
			}
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: raw}}
			native.uuidEntries, native.uuidErr = map[uint]GPUIdentityEntry{3: test.identity}, test.lookupError
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe); err != nil {
				t.Fatal(err)
			}
			result := source.Collect(context.Background(), newTestCycle(t, test.name), CollectionRequest{Health: true})
			if result.HealthError != nil || len(result.HealthReport.GetGroupResults()[0].GetEntityResults()) != 1 || len(result.HealthReport.GroupResults[0].EntityResults[0].GetCollectionErrors()) != 1 {
				t.Fatalf("membership error report lost during identity lookup: %+v", result)
			}
			entry := result.HealthReport.GroupResults[0].EntityResults[0]
			gpu := entry.GetEntity().GetGpu()
			if gpu.GetUuid() != test.identity.UUID || gpu.GetBusId() != test.identity.BusID || entry.GetGroupMember() ||
				!proto.Equal(gpu.GetNative(), raw.GroupResults[0].EntityResults[0].Entity.GetGpu().GetNative()) ||
				!proto.Equal(entry.CollectionErrors[0], raw.GroupResults[0].EntityResults[0].CollectionErrors[0]) {
				t.Fatalf("membership error or native identity changed: %v", entry)
			}
			if native.uuidCalls.Load() != 1 || native.enumerationCalls.Load() != 0 ||
				raw.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "" || raw.GroupResults[0].EntityResults[0].Entity.GetGpu().GetBusId() != "" {
				t.Fatal("error-only enrichment queried discovery or mutated the native-owned report")
			}
		})
	}
}

func TestHealthEntityErrorConnectionLossPreservesReportAndRecovers(t *testing.T) {
	raw := &observation.DCGMHealthReport{
		GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{
			{
				Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true),
				Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemMemory, ErrorCode: 123, Message: "memory fault"}},
			},
			{
				Entity: healthTestGPUTarget(3), GroupMember: proto.Bool(false),
				CollectionErrors: []*observation.DCGMHealthEntityError{{
					Systems: dcgmhealth.SystemMemory,
					Error: &observation.CollectionError{
						Source: Name, Operation: "health membership", SourceError: connectionNotValidStatus,
						Category: observation.CollectionErrorCategoryUnavailable, Detail: "connection lost adding member",
					},
				}},
			},
		}}},
	}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: raw}}
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	cycle := newTestCycle(t, "member-connection-loss")
	result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true, Inventory: true})
	if result.HealthError != nil || result.HealthReport == nil || len(result.HealthReport.GetSystemErrors()) != 0 ||
		len(result.HealthReport.GetGroupResults()[0].GetEntityResults()) != 2 ||
		!proto.Equal(result.HealthReport.GroupResults[0].EntityResults[0], raw.GroupResults[0].EntityResults[0]) ||
		!proto.Equal(result.HealthReport.GroupResults[0].EntityResults[1], raw.GroupResults[0].EntityResults[1]) {
		t.Fatalf("entity connection error discarded the completed health report: %+v", result)
	}
	if result.DiscoveryError.GetSourceError() != connectionNotValidStatus ||
		result.Inventory.GetCollectionError().GetSourceError() != connectionNotValidStatus {
		t.Fatalf("later inventory read ignored the failed session: %+v", result)
	}
	values := source.ReadGPUMetrics(context.Background(), cycle, 2, []string{signalPowerDraw})
	if values[signalPowerDraw].CollectionError.GetSourceError() != connectionNotValidStatus || native.initCalls.Load() != 1 ||
		native.uuidCalls.Load() != 0 || native.enumerationCalls.Load() != 0 || native.inventoryCalls.Load() != 0 || native.readCalls.Load() != 0 {
		t.Fatal("entity connection failure allowed enrichment or further native work in the failed collection")
	}
	native.health.report = &observation.DCGMHealthReport{
		GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)}}}},
	}
	recovered := source.Collect(context.Background(), newTestCycle(t, "after-member-connection-loss"), CollectionRequest{Health: true, Inventory: true})
	if recovered.HealthError != nil || recovered.DiscoveryError != nil || recovered.Inventory.GetCollectionError() != nil ||
		len(recovered.HealthReport.GetGroupResults()[0].GetEntityResults()) != 1 ||
		recovered.HealthReport.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "GPU-2" || len(recovered.HealthReport.GroupResults[0].EntityResults[0].GetCollectionErrors()) != 0 ||
		native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.healthWatchCalls.Load() != 2 ||
		native.health.readCalls.Load() != 2 || native.inventoryCalls.Load() != 1 {
		t.Fatalf("fresh collection did not recover after structured member failure: %+v", recovered)
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
			native := &healthLifecycleNative{health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
				GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{{Entity: test.target, GroupMember: proto.Bool(true)}}}},
			}}}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.KnownSystems); err != nil {
				t.Fatal(err)
			}
			report, failure := source.newCollectionReader(newTestCycle(t, "skip-uuid-"+test.name)).readHealth(context.Background())
			if failure != nil || native.uuidCalls.Load() != 0 || !proto.Equal(report.GroupResults[0].EntityResults[0].Entity, test.target) {
				t.Fatalf("health collection = %v, %v; UUID calls = %d", report, failure, native.uuidCalls.Load())
			}
		})
	}
}

func TestHealthUUIDCancellationPreservesIncidentsAndNativeGate(t *testing.T) {
	gate := &sync.Mutex{}
	native := &healthLifecycleNative{
		health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
			GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{{
				Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true),
				Incidents: []*observation.DCGMHealthIncident{{
					System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(), ErrorCode: 123, Message: "memory incident",
				}},
			}}}},
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
	if failure != nil || len(result.GetGroupResults()[0].GetEntityResults()) != 1 || len(result.GroupResults[0].EntityResults[0].GetIncidents()) != 1 || result.GroupResults[0].EntityResults[0].Incidents[0].GetMessage() != "memory incident" ||
		result.GroupResults[0].EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != 2 || result.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "" {
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
		result := source.Collect(ctx, cycle, CollectionRequest{Health: true})
		finished <- result.HealthReport.GroupResults[0].CollectionError
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
						result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
						finished <- result.HealthReport.GroupResults[0].CollectionError
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

func (native *healthBlockingSetupNative) WatchHealth(systems uint64, gpuID *uint) (healthWatch, error) {
	if native.blockWatch {
		close(native.started)
		<-native.release
	}
	return native.healthLifecycleNative.WatchHealth(systems, gpuID)
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
				result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
				if stage == "watch" {
					finished <- result.HealthReport.GroupResults[0].CollectionError
				} else {
					finished <- result.HealthError
				}
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

func TestHealthGroupsReuseSessionAndKeepMasks(t *testing.T) {
	const gpuSystems = dcgmhealth.SystemMemory | dcgmhealth.SystemPCIe
	const switchSystems = dcgmhealth.NVSwitchSystems
	gpuSince, switchSince := timestamppb.New(time.Unix(100, 0)), timestamppb.New(time.Unix(200, 0))
	native := &healthLifecycleNative{
		health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
			GroupResults: []*observation.DCGMHealthGroupResult{{
				NativeOverallHealth: observation.DCGMHealthResultFail.Enum(), MonitoredSince: gpuSince,
				EntityResults: []*observation.DCGMHealthEntityResult{{
					Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true),
					Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemMemory, ErrorCode: 123}},
				}},
			}},
		}},
		switchHealth: &fakeHealthWatch{report: &observation.DCGMHealthReport{
			GroupResults: []*observation.DCGMHealthGroupResult{{
				NativeOverallHealth: observation.DCGMHealthResultWarn.Enum(), MonitoredSince: switchSince,
				EntityResults: []*observation.DCGMHealthEntityResult{{
					Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3, EntityId: 7}}}}, GroupMember: proto.Bool(true),
					Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemNVSwitchFatal, ErrorCode: 456}},
				}},
			}},
		}},
	}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(gpuSystems | switchSystems); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result := source.Collect(context.Background(), newTestCycle(t, "two-health-groups"), CollectionRequest{Health: true})
		report := result.HealthReport
		if result.HealthError != nil || len(report.GetGroupResults()) != 2 || len(report.GroupResults[0].GetEntityResults()) != 1 || len(report.GroupResults[1].GetEntityResults()) != 1 {
			t.Fatalf("two-group collection = %+v", result)
		}
		gpu, switches := report.GroupResults[0], report.GroupResults[1]
		if gpu.GetEntityKind() != 1 || gpu.GetSystems() != gpuSystems || gpu.GetNativeOverallHealth() != observation.DCGMHealthResultFail || !proto.Equal(gpu.GetMonitoredSince(), gpuSince) ||
			switches.GetEntityKind() != 3 || switches.GetSystems() != switchSystems || switches.GetNativeOverallHealth() != observation.DCGMHealthResultWarn || !proto.Equal(switches.GetMonitoredSince(), switchSince) {
			t.Fatalf("native group masks, results or histories mixed: %v", report)
		}
		if report.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "GPU-2" || report.GroupResults[0].EntityResults[0].Incidents[0].GetErrorCode() != 123 ||
			report.GroupResults[1].EntityResults[0].Entity.GetNvSwitch().GetNative().GetEntityId() != 7 || report.GroupResults[1].EntityResults[0].Incidents[0].GetErrorCode() != 456 {
			t.Fatalf("native findings crossed entity groups: %v", report)
		}
	}
	if native.initCalls.Load() != 1 || native.healthWatchCalls.Load() != 2 || native.health.readCalls.Load() != 2 || native.switchHealth.readCalls.Load() != 2 ||
		!slices.Equal(native.healthMasks, []uint64{gpuSystems, switchSystems}) || len(source.session.gpuHealthWatches) != 1 || source.session.switchHealthWatch == nil {
		t.Fatal("healthy reads did not reuse independent watches on one session")
	}
}

func TestHealthGroupFailuresPreserveOtherGroup(t *testing.T) {
	for _, failedKind := range []uint32{1, 3} {
		for _, stage := range []string{"setup", "check"} {
			t.Run(fmt.Sprintf("kind=%d/%s", failedKind, stage), func(t *testing.T) {
				const requested = dcgmhealth.SystemMemory | dcgmhealth.SystemNVSwitchFatal
				native := &healthLifecycleNative{
					health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
						GroupResults: []*observation.DCGMHealthGroupResult{{
							NativeOverallHealth: observation.DCGMHealthResultPass.Enum(),
							EntityResults:       []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)}},
						}},
					}},
					switchHealth: &fakeHealthWatch{report: &observation.DCGMHealthReport{
						GroupResults: []*observation.DCGMHealthGroupResult{{
							NativeOverallHealth: observation.DCGMHealthResultPass.Enum(),
							EntityResults:       []*observation.DCGMHealthEntityResult{{Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3, EntityId: 7}}}}, GroupMember: proto.Bool(true)}},
						}},
					}},
				}
				index, systems, failedWatch := 0, uint64(dcgmhealth.SystemMemory), native.health
				if failedKind == 3 {
					index, systems, failedWatch = 1, dcgmhealth.SystemNVSwitchFatal, native.switchHealth
				}
				failure := errors.New("group " + stage + " failed")
				if stage == "setup" {
					native.healthSetupErrors = map[uint64]error{systems: failure}
				} else {
					failedWatch.readError = failure
					failedWatch.report.GroupResults[0].NativeOverallHealth = nil
				}
				source := newSource(native, &sync.Mutex{})
				t.Cleanup(func() { _ = source.Close() })
				if err := source.AddHealthToWatch(requested); err != nil {
					t.Fatal(err)
				}
				result := source.Collect(context.Background(), newTestCycle(t, "partial-group"), CollectionRequest{Health: true})
				report := result.HealthReport
				if result.HealthError != nil || len(report.GetGroupResults()) != 2 || len(report.GetSystemErrors()) != 0 ||
					!strings.Contains(report.GroupResults[index].GetCollectionError().GetDetail(), failure.Error()) ||
					report.GroupResults[1-index].GetCollectionError() != nil || report.GroupResults[1-index].NativeOverallHealth == nil {
					t.Fatalf("one group's failure discarded the other: %+v", result)
				}
				wantFailedMembers := 1
				if stage == "setup" && failedKind == 3 {
					wantFailedMembers = 0
				}
				if stage == "setup" && failedKind == 1 && report.GroupResults[index].EntityResults[0].GroupMember != nil {
					t.Fatal("failed GPU allocation fabricated membership")
				}
				if len(report.GroupResults[index].GetEntityResults()) != wantFailedMembers || len(report.GroupResults[1-index].GetEntityResults()) != 1 || failedWatch.report.GroupResults[0].GetCollectionError() != nil {
					t.Fatal("partial collection lost members or mutated the native-owned report")
				}
				delete(native.healthSetupErrors, systems)
				failedWatch.readError = nil
				failedWatch.report.GroupResults[0].NativeOverallHealth = observation.DCGMHealthResultPass.Enum()
				recovered := source.Collect(context.Background(), newTestCycle(t, "after-group-failure"), CollectionRequest{Health: true})
				if recovered.HealthError != nil || len(recovered.HealthReport.GroupResults[0].GetEntityResults()) != 1 || len(recovered.HealthReport.GroupResults[1].GetEntityResults()) != 1 ||
					recovered.HealthReport.GroupResults[0].GetCollectionError() != nil || recovered.HealthReport.GroupResults[1].GetCollectionError() != nil || native.initCalls.Load() != 1 {
					t.Fatalf("next collection did not retry the failed group: %+v", recovered)
				}
				wantWatches := int32(2)
				if stage == "setup" {
					wantWatches++
				}
				if native.healthWatchCalls.Load() != wantWatches {
					t.Fatal("group recovery recreated a usable watch")
				}
			})
		}
	}
}

func TestLaterHealthGroupTimeoutKeepsCompletedGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := &sync.Mutex{}
		started, released := make(chan struct{}), make(chan struct{})
		native := &healthLifecycleNative{
			health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
				GroupResults: []*observation.DCGMHealthGroupResult{{
					NativeOverallHealth: observation.DCGMHealthResultFail.Enum(),
					EntityResults:       []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true), Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemMemory, ErrorCode: 123}}}},
				}},
			}},
			switchHealth:  &fakeHealthWatch{readStarted: started, releaseRead: released},
			cleanupNative: cleanupNative{nativeCallGate: gate},
		}
		release := sync.OnceFunc(func() { close(released) })
		defer release()
		source := newSource(native, gate)
		registerMetricAndHealth(t, source)
		if err := source.AddHealthToWatch(dcgmhealth.SystemNVSwitchFatal); err != nil {
			t.Fatal(err)
		}
		const timeout = 100 * time.Millisecond
		cycle, err := collect.NewCycleWithTimeout("later-health-timeout", time.Now(), timeout)
		if err != nil {
			t.Fatal(err)
		}
		finished := make(chan CollectionResult, 1)
		go func() { finished <- source.Collect(context.Background(), cycle, CollectionRequest{Health: true}) }()
		<-started
		synctest.Wait()
		time.Sleep(timeout)
		result := <-finished
		if result.HealthError != nil || len(result.HealthReport.GetGroupResults()) != 2 || len(result.HealthReport.GetGroupResults()[0].GetEntityResults()) != 1 ||
			result.HealthReport.GroupResults[0].GetCollectionError() != nil ||
			result.HealthReport.GroupResults[1].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryTimeout ||
			result.HealthReport.GroupResults[0].EntityResults[0].Incidents[0].GetErrorCode() != 123 || result.HealthReport.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "" || native.uuidCalls.Load() != 0 {
			t.Fatalf("later group timeout lost completed native findings: %+v", result)
		}
		for _, next := range []*collect.Cycle{cycle, newTestCycle(t, "while-switch-blocked")} {
			values := source.ReadGPUMetrics(context.Background(), next, 2, []string{signalPowerDraw})
			if values[signalPowerDraw].CollectionError == nil || native.readCalls.Load() != 0 {
				t.Fatal("unfinished switch call allowed subsequent metric work")
			}
		}
		release()
		synctest.Wait()
		waitForNativeGate(t, gate)
		native.switchHealth.readStarted = nil
		if same := source.Collect(context.Background(), cycle, CollectionRequest{Health: true}); same.HealthError.GetCategory() != observation.CollectionErrorCategoryTimeout {
			t.Fatalf("timed-out collection resumed after its call completed: %+v", same)
		}
		fresh := source.Collect(context.Background(), newTestCycle(t, "after-switch-timeout"), CollectionRequest{Health: true})
		if fresh.HealthError != nil || fresh.HealthReport.GroupResults[1].GetCollectionError() != nil ||
			fresh.HealthReport.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "GPU-2" || native.initCalls.Load() != 1 || native.healthWatchCalls.Load() != 2 {
			t.Fatalf("fresh collection did not reuse the healthy session: %+v", fresh)
		}
		if err := source.Close(); err != nil || native.health.cleanupCalls.Load() != 1 || native.switchHealth.cleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
			t.Fatalf("timeout recovery lost watch cleanup: %v", err)
		}
	})
}

func TestHealthGroupsKeepOverlappingNativeEntitiesSeparate(t *testing.T) {
	target := &observation.ObservationTarget{Kind: &observation.ObservationTarget_NativeEntity{NativeEntity: &observation.NativeEntityRef{Source: Name, EntityKind: 71, EntityId: 9}}}
	gpuReport := &observation.DCGMHealthReport{
		GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{{
			Entity: target, GroupMember: proto.Bool(false), Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemMemory, ErrorCode: 123}},
			CollectionErrors: []*observation.DCGMHealthEntityError{{Systems: dcgmhealth.SystemMemory, Error: &observation.CollectionError{Detail: "GPU-side error"}}},
		}}}},
	}
	switchReport := &observation.DCGMHealthReport{
		GroupResults: []*observation.DCGMHealthGroupResult{{EntityResults: []*observation.DCGMHealthEntityResult{{
			Entity: proto.Clone(target).(*observation.ObservationTarget), GroupMember: proto.Bool(true),
			Incidents:        []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemNVSwitchFatal, ErrorCode: 456}},
			CollectionErrors: []*observation.DCGMHealthEntityError{{Systems: dcgmhealth.SystemNVSwitchFatal, Error: &observation.CollectionError{Detail: "switch-side error"}}},
		}}}},
	}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: gpuReport}, switchHealth: &fakeHealthWatch{report: switchReport}}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory | dcgmhealth.SystemNVSwitchFatal); err != nil {
		t.Fatal(err)
	}
	result := source.Collect(context.Background(), newTestCycle(t, "shared-native-entity"), CollectionRequest{Health: true})
	if result.HealthError != nil || len(result.HealthReport.GetGroupResults()) != 2 || len(result.HealthReport.GroupResults[0].GetEntityResults()) != 1 || len(result.HealthReport.GroupResults[1].GetEntityResults()) != 1 || native.uuidCalls.Load() != 0 {
		t.Fatalf("overlapping native entities lost findings: %+v", result)
	}
	for index, original := range []*observation.DCGMHealthReport{gpuReport, switchReport} {
		entry := result.HealthReport.GroupResults[index].EntityResults[0]
		if !proto.Equal(entry, original.GroupResults[0].EntityResults[0]) {
			t.Fatalf("appending changed group %d's membership or findings: %v", index, entry)
		}
		entry.GroupMember = proto.Bool(!entry.GetGroupMember())
		entry.Incidents[0].ErrorCode = 999
		entry.CollectionErrors[0].Error.Detail = "changed"
	}
	if gpuReport.GroupResults[0].EntityResults[0].GetGroupMember() || !switchReport.GroupResults[0].EntityResults[0].GetGroupMember() ||
		gpuReport.GroupResults[0].EntityResults[0].Incidents[0].GetErrorCode() != 123 || switchReport.GroupResults[0].EntityResults[0].Incidents[0].GetErrorCode() != 456 ||
		gpuReport.GroupResults[0].EntityResults[0].CollectionErrors[0].Error.GetDetail() != "GPU-side error" || switchReport.GroupResults[0].EntityResults[0].CollectionErrors[0].Error.GetDetail() != "switch-side error" {
		t.Fatal("appending aliased a native-owned report")
	}
}

func TestHealthGroupConnectionStatusStopsSessionAndFreshCollectionRecovers(t *testing.T) {
	failure := &observation.CollectionError{Source: Name, Operation: "health check", SourceError: connectionNotValidStatus, Category: observation.CollectionErrorCategoryUnavailable, Detail: "group connection lost"}
	native := &healthLifecycleNative{health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
		GroupResults: []*observation.DCGMHealthGroupResult{{
			CollectionError: failure,
			EntityResults:   []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)}},
		}},
	}}}
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemNVSwitchFatal); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "group-connection-loss")
	result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
	if result.HealthError != nil || len(result.HealthReport.GetGroupResults()) != 2 || len(result.HealthReport.GetGroupResults()[0].GetEntityResults()) != 1 ||
		!proto.Equal(result.HealthReport.GroupResults[0].GetCollectionError(), failure) ||
		result.HealthReport.GroupResults[1].GetCollectionError().GetSourceError() != connectionNotValidStatus || native.healthWatchCalls.Load() != 1 || native.uuidCalls.Load() != 0 {
		t.Fatalf("group connection status did not stop later native operations: %+v", result)
	}
	if same := source.Collect(context.Background(), cycle, CollectionRequest{Health: true}); same.HealthError.GetSourceError() != connectionNotValidStatus || native.health.readCalls.Load() != 1 {
		t.Fatalf("failed collection resumed native work: %+v", same)
	}
	native.health = &fakeHealthWatch{}
	fresh := source.Collect(context.Background(), newTestCycle(t, "after-group-connection-loss"), CollectionRequest{Health: true})
	if fresh.HealthError != nil || len(fresh.HealthReport.GetGroupResults()) != 2 || fresh.HealthReport.GroupResults[0].GetCollectionError() != nil || fresh.HealthReport.GroupResults[1].GetCollectionError() != nil ||
		native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.healthWatchCalls.Load() != 3 {
		t.Fatalf("new collection did not rebuild independent watches: %+v", fresh)
	}
}

func TestBothHealthGroupsFailKeepKnownGPUTargets(t *testing.T) {
	for _, setup := range []bool{true, false} {
		t.Run(map[bool]string{true: "setup failures without reports", false: "check failures with verified members"}[setup], func(t *testing.T) {
			gpuError, switchError := errors.New("GPU health failed"), errors.New("switch health failed")
			native := &healthLifecycleNative{
				health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
					GroupResults: []*observation.DCGMHealthGroupResult{{
						EntityResults: []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true)}},
					}},
				}},
				switchHealth: &fakeHealthWatch{report: &observation.DCGMHealthReport{
					GroupResults: []*observation.DCGMHealthGroupResult{{
						EntityResults: []*observation.DCGMHealthEntityResult{{
							Entity: &observation.ObservationTarget{Kind: &observation.ObservationTarget_NVSwitch{NvSwitch: &observation.NVSwitchRef{Native: &observation.NativeEntityRef{Source: Name, EntityKind: 3, EntityId: 7}}}}, GroupMember: proto.Bool(true),
						}},
					}},
				}},
			}
			if setup {
				native.healthSetupErrors = map[uint64]error{dcgmhealth.SystemMemory: gpuError, dcgmhealth.SystemNVSwitchFatal: switchError}
			} else {
				native.health.readError, native.switchHealth.readError = gpuError, switchError
			}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.SystemMemory | dcgmhealth.SystemNVSwitchFatal); err != nil {
				t.Fatal(err)
			}
			result := source.Collect(context.Background(), newTestCycle(t, "both-groups-fail"), CollectionRequest{Health: true})
			if native.initCalls.Load() != 1 || native.healthWatchCalls.Load() != 2 {
				t.Fatal("group failures replaced the shared client or skipped a kind")
			}
			if setup {
				report := result.HealthReport
				if result.HealthError != nil || len(report.GetGroupResults()) != 2 || len(report.GroupResults[0].GetEntityResults()) != 1 || len(report.GroupResults[1].GetEntityResults()) != 0 ||
					!strings.Contains(report.GroupResults[0].GetCollectionError().GetDetail(), gpuError.Error()) || !strings.Contains(report.GroupResults[1].GetCollectionError().GetDetail(), switchError.Error()) ||
					report.GroupResults[0].EntityResults[0].GroupMember != nil || report.GroupResults[0].EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != 2 || report.GetSystemsWithoutTargets() != 0 ||
					native.health.readCalls.Load() != 0 || native.switchHealth.readCalls.Load() != 0 || native.uuidCalls.Load() != 1 {
					t.Fatalf("setup failures lost discovered GPU context: %+v", result)
				}
				return
			}
			report := result.HealthReport
			if result.HealthError != nil || len(report.GetGroupResults()) != 2 || len(report.GroupResults[0].GetEntityResults()) != 1 || len(report.GroupResults[1].GetEntityResults()) != 1 || report.GetSystemsWithoutTargets() != 0 ||
				native.health.readCalls.Load() != 1 || native.switchHealth.readCalls.Load() != 1 || native.uuidCalls.Load() != 1 {
				t.Fatalf("failed checks discarded verified partial reports: %+v", result)
			}
			for index, want := range []error{gpuError, switchError} {
				group := report.GroupResults[index]
				if group.NativeOverallHealth != nil ||
					!strings.Contains(group.GetCollectionError().GetDetail(), want.Error()) || len(group.GetEntityResults()) != 1 || !group.EntityResults[0].GetGroupMember() {
					t.Fatalf("failed check became a native pass or lost group coverage: %v", report)
				}
			}
			if report.GroupResults[0].EntityResults[0].Entity.GetGpu().GetUuid() != "GPU-2" || report.GroupResults[1].EntityResults[0].Entity.GetNvSwitch().GetNative().GetEntityId() != 7 {
				t.Fatalf("failed checks lost member identity: %v", report)
			}
		})
	}
}

func TestHealthIdentityEnrichmentDeduplicatesAcrossGroups(t *testing.T) {
	report := &observation.DCGMHealthReport{GroupResults: []*observation.DCGMHealthGroupResult{
		{EntityKind: 1, Systems: dcgmhealth.SystemMemory, EntityResults: []*observation.DCGMHealthEntityResult{{
			Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(true),
		}}},
		{EntityKind: 3, Systems: dcgmhealth.SystemNVSwitchFatal, EntityResults: []*observation.DCGMHealthEntityResult{{
			Entity: healthTestGPUTarget(2), GroupMember: proto.Bool(false),
			Incidents: []*observation.DCGMHealthIncident{{System: dcgmhealth.SystemNVSwitchFatal, ErrorCode: 123}},
		}}},
	}}
	native := &fakeNative{
		uuidReadIndices: make(chan []uint, 1),
		uuidEntries:     map[uint]GPUIdentityEntry{2: {UUID: "GPU-2", BusID: "0000:53:00.0"}},
	}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	reader := source.newCollectionReader(newTestCycle(t, "group-identity-dedup"))
	reader.enrichHealthIdentities(context.Background(), report)
	if native.uuidCalls.Load() != 1 || native.enumerationCalls.Load() != 0 {
		t.Fatal("group identity enrichment repeated lookup or discovered GPUs")
	}
	if ids := <-native.uuidReadIndices; !slices.Equal(ids, []uint{2}) {
		t.Fatalf("group identity lookup IDs = %v, want each GPU once", ids)
	}
	for _, group := range report.GroupResults {
		gpu := group.EntityResults[0].Entity.GetGpu()
		if gpu.GetUuid() != "GPU-2" || gpu.GetBusId() != "0000:53:00.0" || gpu.GetNative().GetEntityId() != 2 {
			t.Fatalf("group identity was not enriched: %v", group)
		}
	}
	if !report.GroupResults[0].EntityResults[0].GetGroupMember() || report.GroupResults[1].EntityResults[0].GetGroupMember() ||
		len(report.GroupResults[0].EntityResults[0].GetIncidents()) != 0 || report.GroupResults[1].EntityResults[0].Incidents[0].GetErrorCode() != 123 {
		t.Fatal("identity enrichment mixed separate groups' findings or membership")
	}
}

func healthTestGPUWatch(id uint32) *fakeHealthWatch {
	return &fakeHealthWatch{report: &observation.DCGMHealthReport{GroupResults: []*observation.DCGMHealthGroupResult{{
		EntityKind: gpuEntityKind, Systems: dcgmhealth.SystemMemory,
		NativeOverallHealth: observation.DCGMHealthResultPass.Enum(), MonitoredSince: timestamppb.New(time.Unix(int64(100+id), 0)),
		EntityResults: []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(id), GroupMember: proto.Bool(true)}},
	}}}}
}

func TestGPUHealthFailuresStayWithTheirGPU(t *testing.T) {
	for _, scenario := range []string{"check failure", "one allocation limit", "all allocation limits"} {
		t.Run(scenario, func(t *testing.T) {
			native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{0: healthTestGPUWatch(0), 1: healthTestGPUWatch(1), 2: healthTestGPUWatch(2)}}
			limit := &dcgmError{category: observation.CollectionErrorCategoryUnavailable, sourceError: "DCGM_ST_MAX_LIMIT", detail: "native group capacity exhausted"}
			if scenario == "check failure" {
				native.gpuHealth[1].readError = errors.New("GPU 1 sample read failed")
				native.gpuHealth[1].report.GroupResults[0].NativeOverallHealth = nil
			} else {
				native.gpuSetupErrors = map[uint]error{1: limit}
				if scenario == "all allocation limits" {
					native.gpuSetupErrors[0], native.gpuSetupErrors[2] = limit, limit
				}
			}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
				t.Fatal(err)
			}
			result := source.Collect(context.Background(), newTestCycle(t, scenario), CollectionRequest{Health: true})
			if result.HealthError != nil || len(result.HealthReport.GetGroupResults()) != 3 || result.HealthReport.GetSystemsWithoutTargets() != 0 ||
				native.healthEnumerationCalls.Load() != 1 || native.initCalls.Load() != 1 || native.healthWatchCalls.Load() != 3 {
				t.Fatalf("GPU discovery or allocation failure discarded target context: %+v", result)
			}
			for id, group := range result.HealthReport.GroupResults {
				if len(group.GetEntityResults()) != 1 || group.EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != uint32(id) {
					t.Fatalf("group failure lost GPU attribution: %v", group)
				}
				failed := id == 1 || scenario == "all allocation limits"
				if !failed {
					if group.CollectionError != nil || group.NativeOverallHealth == nil || group.GetNativeOverallHealth() != observation.DCGMHealthResultPass || native.gpuHealth[uint(id)].readCalls.Load() != 1 {
						t.Fatalf("another GPU's failure hid a usable result: %v", group)
					}
				} else if group.CollectionError == nil || group.NativeOverallHealth != nil {
					t.Fatalf("failed GPU was reported as a native pass: %v", group)
				} else if scenario != "check failure" && (group.EntityResults[0].GroupMember != nil || group.CollectionError.GetSourceError() != "DCGM_ST_MAX_LIMIT" || native.gpuHealth[uint(id)].readCalls.Load() != 0) {
					t.Fatalf("failed allocation fabricated membership or entered a check: %v", group)
				}
			}
		})
	}
}

func TestGPUHealthDiscoveryFailureKeepsKnownGroupsAndReturnReusesOthers(t *testing.T) {
	native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{0: healthTestGPUWatch(0), 1: healthTestGPUWatch(1), 2: healthTestGPUWatch(2)}}
	native.healthIndices = []uint{0, 1}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
	first := source.Collect(context.Background(), newTestCycle(t, "GPU health first"), CollectionRequest{Health: true})
	if first.HealthError != nil || len(first.HealthReport.GetGroupResults()) != 2 {
		t.Fatal(first)
	}
	native.healthEnumerationErr = errors.New("active GPU discovery failed")
	partial := source.Collect(context.Background(), newTestCycle(t, "GPU health discovery failed"), CollectionRequest{Health: true})
	if partial.HealthError != nil || len(partial.HealthReport.GetGroupResults()) != 2 || partial.HealthReport.GetSystemsWithoutTargets() != 0 ||
		!strings.Contains(partial.HealthReport.GetSystemErrors()[dcgmhealth.SystemMemory].GetDetail(), "active GPU discovery failed") || native.healthWatchCalls.Load() != 2 ||
		native.gpuHealth[0].readCalls.Load() != 2 || native.gpuHealth[1].readCalls.Load() != 2 || native.gpuHealth[1].cleanupCalls.Load() != 0 {
		t.Fatalf("discovery error discarded findings or queued known GPU watches for cleanup: %+v", partial)
	}
	native.healthEnumerationErr, native.healthIndices = nil, []uint{0, 2}
	third := source.Collect(context.Background(), newTestCycle(t, "GPU health replacement"), CollectionRequest{Health: true})
	if third.HealthError != nil || len(third.HealthReport.GetGroupResults()) != 2 || len(third.HealthReport.GetSystemErrors()) != 0 ||
		third.HealthReport.GroupResults[1].EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != 2 ||
		!proto.Equal(third.HealthReport.GroupResults[0].MonitoredSince, first.HealthReport.GroupResults[0].MonitoredSince) ||
		native.healthWatchCalls.Load() != 3 || native.healthEnumerationCalls.Load() != 3 || native.gpuHealth[1].cleanupCalls.Load() != 1 || native.gpuHealth[1].readCalls.Load() != 2 ||
		len(source.session.gpuHealthWatches) != 2 || len(source.session.pendingGPUHealthCleanup) != 0 {
		t.Fatalf("successful discovery did not clean up only the absent GPU: %+v", third)
	}
}

func TestGPUHealthCleanupRetriesBeforeStaleReaderCanReplaceWatch(t *testing.T) {
	old := healthTestGPUWatch(1)
	native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{0: healthTestGPUWatch(0), 1: old}}
	native.healthIndices = []uint{0, 1}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
	if result := source.Collect(context.Background(), newTestCycle(t, "before cleanup"), CollectionRequest{Health: true}); result.HealthError != nil {
		t.Fatal(result)
	}
	stale := source.newCollectionReader(newTestCycle(t, "stale discovery"))
	if discovery, err := stale.discoverHealthGPUs(context.Background()); err != nil || !slices.Equal(discovery.ids, []uint{0, 1}) {
		t.Fatalf("stale reader did not capture the original IDs: %v, %v", discovery.ids, err)
	}
	old.cleanupError = errors.New("GPU 1 group destruction failed")
	native.healthIndices = []uint{0}
	partial := source.Collect(context.Background(), newTestCycle(t, "cleanup failure"), CollectionRequest{Health: true})
	if partial.HealthError != nil || len(partial.HealthReport.GetGroupResults()) != 2 || partial.HealthReport.GetSystemsWithoutTargets() != 0 ||
		partial.HealthReport.GroupResults[0].CollectionError != nil || partial.HealthReport.GroupResults[1].CollectionError == nil || partial.HealthReport.GroupResults[1].EntityResults[0].GroupMember != nil ||
		source.session.pendingGPUHealthCleanup[1] != old || source.session.gpuHealthWatches[1] != nil {
		t.Fatalf("cleanup failure lost ownership or usable GPU coverage: %+v", partial)
	}
	id := uint(1)
	if report, err := stale.readHealthGroup(context.Background(), dcgmhealth.SystemMemory, &id); report != nil || err == nil ||
		native.healthWatchCalls.Load() != 2 || source.session.pendingGPUHealthCleanup[1] != old || source.session.gpuHealthWatches[1] != nil {
		t.Fatalf("stale discovery replaced a group whose cleanup is pending: %v, %v", report, err)
	}
	native.healthIndices = []uint{0, 1}
	if retry := source.Collect(context.Background(), newTestCycle(t, "return before cleanup"), CollectionRequest{Health: true}); retry.HealthError != nil ||
		native.healthWatchCalls.Load() != 2 || old.readCalls.Load() != 1 || source.session.pendingGPUHealthCleanup[1] != old {
		t.Fatalf("returning GPU reused a closed watch: %+v", retry)
	}
	old.cleanupError = nil
	replacement := healthTestGPUWatch(1)
	native.gpuHealth[1] = replacement
	recovered := source.Collect(context.Background(), newTestCycle(t, "return after cleanup"), CollectionRequest{Health: true})
	if recovered.HealthError != nil || len(recovered.HealthReport.GetGroupResults()) != 2 || recovered.HealthReport.GroupResults[1].CollectionError != nil ||
		native.healthWatchCalls.Load() != 3 || old.cleanupCalls.Load() != 4 || old.readCalls.Load() != 1 || replacement.readCalls.Load() != 1 || len(source.session.pendingGPUHealthCleanup) != 0 {
		t.Fatalf("cleanup retry did not permit a fresh GPU watch: %+v", recovered)
	}
	if err := source.Close(); err != nil || old.cleanupCalls.Load() != 4 || replacement.cleanupCalls.Load() != 1 || native.gpuHealth[0].cleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
		t.Fatalf("pending or replacement watch cleanup ownership was lost: %v", err)
	}
}

func TestGPUHealthCleanupFailureWithNoActiveGPUs(t *testing.T) {
	watch := healthTestGPUWatch(1)
	native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{1: watch}}
	native.healthIndices = []uint{1}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
	if initial := source.Collect(context.Background(), newTestCycle(t, "before GPU disappears"), CollectionRequest{Health: true}); initial.HealthError != nil {
		t.Fatal(initial)
	}

	native.healthIndices = []uint{}
	watch.cleanupError = errors.New("GPU group destruction failed")
	failed := source.Collect(context.Background(), newTestCycle(t, "no GPUs with cleanup failure"), CollectionRequest{Health: true})
	if failed.HealthError != nil || len(failed.HealthReport.GetGroupResults()) != 1 || failed.HealthReport.GetSystemsWithoutTargets() != 0 {
		t.Fatalf("cleanup failure became a source error or target-free report: %+v", failed)
	}
	group := failed.HealthReport.GroupResults[0]
	if !strings.Contains(group.GetCollectionError().GetDetail(), watch.cleanupError.Error()) || len(group.GetEntityResults()) != 1 ||
		group.EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != 1 || group.EntityResults[0].GroupMember != nil {
		t.Fatalf("cleanup failure lost its GPU or claimed verified membership: %+v", group)
	}

	watch.cleanupError = nil
	recovered := source.Collect(context.Background(), newTestCycle(t, "no GPUs after cleanup"), CollectionRequest{Health: true})
	if recovered.HealthError != nil || len(recovered.HealthReport.GetGroupResults()) != 0 || recovered.HealthReport.GetSystemsWithoutTargets() != dcgmhealth.SystemMemory ||
		len(source.session.pendingGPUHealthCleanup) != 0 || watch.cleanupCalls.Load() != 2 {
		t.Fatalf("successful cleanup did not restore target-free reporting: %+v", recovered)
	}
}

func TestGPUHealthCleanupConnectionLossStopsNativeWork(t *testing.T) {
	native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{0: healthTestGPUWatch(0), 1: healthTestGPUWatch(1), 2: healthTestGPUWatch(2)}}
	native.healthIndices = []uint{0, 1, 2}
	source := newSource(native, &sync.Mutex{})
	registerMetricAndHealth(t, source)
	t.Cleanup(func() { _ = source.Close() })
	if result := source.Collect(context.Background(), newTestCycle(t, "before cleanup connection loss"), CollectionRequest{Health: true}); result.HealthError != nil {
		t.Fatal(result)
	}
	oldZero := native.gpuHealth[0]
	native.gpuHealth[1].cleanupError = connectionLoss()
	native.healthIndices = []uint{0}
	cycle := newTestCycle(t, "cleanup connection loss")
	partial := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
	if partial.HealthError != nil || partial.HealthReport.GetSystemsWithoutTargets() != 0 || native.gpuHealth[1].cleanupCalls.Load() != 1 ||
		native.gpuHealth[2].cleanupCalls.Load() != 0 || oldZero.readCalls.Load() != 1 || native.uuidCalls.Load() != 1 || source.session.connectionFailure == nil {
		t.Fatalf("connection loss during cleanup allowed later native work: %+v", partial)
	}
	if field := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})[signalPowerDraw]; field.CollectionError.GetSourceError() != connectionNotValidStatus || native.readCalls.Load() != 0 {
		t.Fatalf("cleanup connection loss did not stop same-collection metrics: %v", field)
	}
	native.gpuHealth[0] = healthTestGPUWatch(0)
	fresh := source.Collect(context.Background(), newTestCycle(t, "after cleanup connection loss"), CollectionRequest{Health: true})
	if fresh.HealthError != nil || len(fresh.HealthReport.GetGroupResults()) != 1 || fresh.HealthReport.GroupResults[0].CollectionError != nil ||
		native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.healthWatchCalls.Load() != 4 || oldZero.cleanupCalls.Load() != 0 || native.gpuHealth[2].cleanupCalls.Load() != 0 {
		t.Fatalf("fresh collection did not recover after cleanup connection loss: %+v", fresh)
	}
}

func TestGPUHealthDiscoveryEmptyAndDuplicates(t *testing.T) {
	for _, ids := range [][]uint{{}, {2, 2, 0, 2, 0}} {
		t.Run(fmt.Sprint(ids), func(t *testing.T) {
			native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{0: healthTestGPUWatch(0), 2: healthTestGPUWatch(2)}}
			native.healthIndices = ids
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
				t.Fatal(err)
			}
			result := source.Collect(context.Background(), newTestCycle(t, "GPU discovery"), CollectionRequest{Health: true})
			if result.HealthError != nil || native.healthEnumerationCalls.Load() != 1 {
				t.Fatal(result)
			}
			if len(ids) == 0 {
				if result.HealthReport.GetSystemsWithoutTargets() != dcgmhealth.SystemMemory || len(result.HealthReport.GetGroupResults()) != 0 || native.healthWatchCalls.Load() != 0 {
					t.Fatalf("empty discovery allocated a GPU group: %+v", result)
				}
			} else if len(result.HealthReport.GetGroupResults()) != 2 || native.healthWatchCalls.Load() != 2 || native.gpuHealth[0].readCalls.Load() != 1 || native.gpuHealth[2].readCalls.Load() != 1 ||
				result.HealthReport.GroupResults[0].EntityResults[0].Entity.GetGpu().GetNative().GetEntityId() != 2 {
				t.Fatalf("discovery duplicates repeated checks or changed discovery order: %+v", result)
			}
		})
	}
}

func TestGPUHealthNoTargetsRequiresEveryGPUGroupToBeEmpty(t *testing.T) {
	native := &healthLifecycleNative{gpuHealth: map[uint]*fakeHealthWatch{
		0: {report: &observation.DCGMHealthReport{
			SystemsWithoutTargets: dcgmhealth.SystemMemory,
			GroupResults:          []*observation.DCGMHealthGroupResult{{EntityKind: gpuEntityKind, Systems: dcgmhealth.SystemMemory}},
		}},
		1: healthTestGPUWatch(1),
	}}
	native.healthIndices = []uint{0, 1}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
	result := source.Collect(context.Background(), newTestCycle(t, "one empty GPU group"), CollectionRequest{Health: true})
	report := result.HealthReport
	if result.HealthError != nil || len(report.GetGroupResults()) != 2 || report.GetSystemsWithoutTargets() != 0 ||
		len(report.GroupResults[0].GetEntityResults()) != 0 || len(report.GroupResults[1].GetEntityResults()) != 1 ||
		report.GroupResults[1].NativeOverallHealth == nil || report.GroupResults[1].GetNativeOverallHealth() != observation.DCGMHealthResultPass ||
		!report.GroupResults[1].EntityResults[0].GetGroupMember() || report.GroupResults[1].EntityResults[0].Entity.GetGpu().GetUuid() != "GPU-1" ||
		len(report.GroupResults[1].EntityResults[0].GetIncidents()) != 0 {
		t.Fatalf("one empty GPU group hid another GPU's successful coverage: %+v", result)
	}
}
