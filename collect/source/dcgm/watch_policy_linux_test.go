//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

type policyNativeWatchRecord struct {
	fieldIDs   []godcgm.Short
	interval   int64
	maxAge     float64
	maxSamples int32
}

type policyNativeWatchTrace struct {
	createError       error
	watchError        error
	unwatchError      error
	destroyError      error
	groups            map[uintptr][]godcgm.Short
	unwatches         map[uintptr]int
	destroys          map[uintptr]int
	records           []policyNativeWatchRecord
	names             []string
	createCalls       int
	failCreateAt      int
	failWatchAt       int
	failUnwatchHandle uintptr
	failDestroyHandle uintptr
}

func newPolicyNativeWatchRunner(t *testing.T, policy *WatchPolicy) (func([]string) (func() error, error), *policyNativeWatchTrace) {
	t.Helper()
	trace := &policyNativeWatchTrace{
		createError:  errors.New("field-group pool exhausted"),
		watchError:   errors.New("watch failed"),
		unwatchError: errors.New("unwatch failed"),
		destroyError: errors.New("destroy failed"),
		groups:       make(map[uintptr][]godcgm.Short),
		unwatches:    make(map[uintptr]int),
		destroys:     make(map[uintptr]int),
	}
	library := newNative(StandaloneOptions{WatchPolicy: policy}, standaloneMode).(*nativeLibrary)
	ops := metricWatchOperations{
		create: func(name string, fieldIDs []godcgm.Short) (godcgm.FieldHandle, error) {
			trace.createCalls++
			var handle godcgm.FieldHandle
			if trace.createCalls == trace.failCreateAt {
				return handle, trace.createError
			}
			handle.SetHandle(uintptr(trace.createCalls))
			trace.groups[handle.GetHandle()] = slices.Clone(fieldIDs)
			trace.names = append(trace.names, name)
			return handle, nil
		},
		watchEx: func(fieldGroup godcgm.FieldHandle, gpuGroup godcgm.GroupHandle, interval int64, maxAge float64, maxSamples int32) error {
			if gpuGroup != godcgm.GroupAllGPUs() {
				t.Fatal("watch used a group other than DCGM's all-GPU group")
			}
			fields, owned := trace.groups[fieldGroup.GetHandle()]
			if !owned {
				t.Fatal("watched a field group that was not created by this source")
			}
			trace.records = append(trace.records, policyNativeWatchRecord{
				fieldIDs: slices.Clone(fields), interval: interval, maxAge: maxAge, maxSamples: maxSamples,
			})
			if len(trace.records) == trace.failWatchAt {
				return trace.watchError
			}
			return nil
		},
		unwatch: func(fieldGroup godcgm.FieldHandle, gpuGroup godcgm.GroupHandle) error {
			if gpuGroup != godcgm.GroupAllGPUs() {
				t.Fatal("unwatch used a different entity group")
			}
			handle := fieldGroup.GetHandle()
			trace.unwatches[handle]++
			if handle == trace.failUnwatchHandle {
				return trace.unwatchError
			}
			return nil
		},
		destroy: func(fieldGroup godcgm.FieldHandle) error {
			handle := fieldGroup.GetHandle()
			if _, owned := trace.groups[handle]; !owned {
				t.Fatalf("destroyed unowned field group %d", handle)
			}
			trace.destroys[handle]++
			if handle == trace.failDestroyHandle {
				return trace.destroyError
			}
			delete(trace.groups, handle)
			return nil
		},
	}
	return func(signalIDs []string) (func() error, error) {
		groups, err := library.MetricWatchGroups(signalIDs)
		if err != nil {
			return nil, err
		}
		return startMetricWatches("test-policy", groups, ops)
	}, trace
}

func TestNativeWatchPolicyDefaultPreservesSettingsAndFieldOrder(t *testing.T) {
	watchMetrics, trace := newPolicyNativeWatchRunner(t, nil)
	cleanup, err := watchMetrics([]string{signalPowerDraw, signalTemperature})
	if err != nil || cleanup == nil {
		t.Fatalf("WatchMetrics() = cleanup present %v, %v", cleanup != nil, err)
	}
	if trace.createCalls != 1 || len(trace.records) != 1 {
		t.Fatalf("default watch created %d groups and recorded %d calls, want one each", trace.createCalls, len(trace.records))
	}
	record := trace.records[0]
	if record.interval != 30_000_000 || record.maxAge != 0 || record.maxSamples != 1 {
		t.Fatalf("default watch settings = %+v, want 30000000 microseconds, no maximum age, one sample", record)
	}
	wantFields := []godcgm.Short{fieldsBySignal[signalPowerDraw].fieldID, fieldsBySignal[signalTemperature].fieldID}
	if !slices.Equal(record.fieldIDs, wantFields) {
		t.Fatalf("default field order = %v, want %v", record.fieldIDs, wantFields)
	}
	if trace.names[0] != "test-policy-fields" {
		t.Fatalf("default field-group name = %q, want test-policy-fields", trace.names[0])
	}
	if err := cleanup(); err != nil || len(trace.groups) != 0 {
		t.Fatalf("default cleanup = %v, remaining groups = %v", err, trace.groups)
	}
}

func TestNativeWatchPolicyCanonicalAliasesAndCacheSettings(t *testing.T) {
	clockReasons := gpusignals.ClockEventReasons.ID()
	hardwareSlowdown := gpusignals.HardwareSlowdownActive.ID()
	watchMetrics, trace := newPolicyNativeWatchRunner(t, &WatchPolicy{
		DefaultInterval: 30 * time.Second,
		SignalIntervals: map[string]time.Duration{clockReasons: time.Minute, hardwareSlowdown: 250 * time.Millisecond},
	})
	cleanup, err := watchMetrics([]string{clockReasons, signalPowerDraw, hardwareSlowdown})
	if err != nil || cleanup == nil {
		t.Fatalf("WatchMetrics() = cleanup present %v, %v", cleanup != nil, err)
	}
	if len(trace.records) != 2 || trace.records[0].interval != 250000 || trace.records[1].interval != 30000000 {
		t.Fatalf("custom intervals = %+v, want 250000 and 30000000 microseconds", trace.records)
	}
	for i, record := range trace.records {
		if record.maxAge != 0 || record.maxSamples != 1 {
			t.Fatalf("group %d cache settings = %+v", i, record)
		}
		if len(record.fieldIDs) != 1 {
			t.Fatalf("group %d fields = %v; aliases should watch one field", i, record.fieldIDs)
		}
		if !strings.HasSuffix(trace.names[i], fmt.Sprintf("-%d-fields", i)) {
			t.Fatalf("group %d name = %q", i, trace.names[i])
		}
	}
	if trace.records[0].fieldIDs[0] != fieldsBySignal[clockReasons].fieldID || trace.records[1].fieldIDs[0] != fieldsBySignal[signalPowerDraw].fieldID {
		t.Fatalf("custom field grouping = %+v", trace.records)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if len(trace.groups) != 0 || trace.unwatches[1] != 1 || trace.unwatches[2] != 1 || trace.destroys[1] != 1 || trace.destroys[2] != 1 {
		t.Fatalf("cleanup did not release each group once: %+v", trace)
	}
}

func TestNativeWatchPolicyPoolFailureRollsBackEveryStep(t *testing.T) {
	for _, failingStep := range []string{"unwatch", "destroy", "both"} {
		t.Run(failingStep, func(t *testing.T) {
			watchMetrics, trace := newPolicyNativeWatchRunner(t, &WatchPolicy{
				DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{signalTemperature: 2 * time.Second},
			})
			trace.failCreateAt = 2
			if failingStep != "destroy" {
				trace.failUnwatchHandle = 1
			}
			if failingStep != "unwatch" {
				trace.failDestroyHandle = 1
			}
			cleanup, err := watchMetrics([]string{signalPowerDraw, signalTemperature})
			if cleanup != nil || !errors.Is(err, trace.createError) {
				t.Fatalf("partial setup cleanup present = %v, error = %v", cleanup != nil, err)
			}
			if trace.failUnwatchHandle != 0 && !errors.Is(err, trace.unwatchError) {
				t.Fatalf("partial setup error lost unwatch failure: %v", err)
			}
			if trace.failDestroyHandle != 0 && !errors.Is(err, trace.destroyError) {
				t.Fatalf("partial setup error lost destroy failure: %v", err)
			}
			wantRemaining := 0
			if trace.failDestroyHandle != 0 {
				wantRemaining = 1
			}
			if trace.createCalls != 2 || len(trace.records) != 1 || len(trace.groups) != wantRemaining {
				t.Fatalf("partial setup rollback left unexpected resources: %+v", trace)
			}
			if trace.unwatches[1] != 1 || trace.destroys[1] != 1 || len(trace.unwatches) != 1 || len(trace.destroys) != 1 {
				t.Fatalf("partial setup rollback did not attempt each owned step once: %+v", trace)
			}
		})
	}
}

func TestNativeWatchPolicyLaterWatchFailureCleansEveryGroup(t *testing.T) {
	watchMetrics, trace := newPolicyNativeWatchRunner(t, &WatchPolicy{
		DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{signalTemperature: 2 * time.Second},
	})
	trace.failWatchAt = 2
	cleanup, err := watchMetrics([]string{signalPowerDraw, signalTemperature})
	if err == nil || cleanup != nil || !strings.Contains(err.Error(), trace.watchError.Error()) {
		t.Fatalf("failed setup cleanup present = %v, error = %v", cleanup != nil, err)
	}
	if len(trace.groups) != 0 || len(trace.records) != 2 || trace.unwatches[1] != 1 || trace.unwatches[2] != 1 || trace.destroys[1] != 1 || trace.destroys[2] != 1 {
		t.Fatalf("watch failure leaked or repeated cleanup: %+v", trace)
	}
}

func TestNativeWatchPolicyRollbackCleansEveryGroupAfterErrors(t *testing.T) {
	watchMetrics, trace := newPolicyNativeWatchRunner(t, &WatchPolicy{
		DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{signalTemperature: 2 * time.Second},
	})
	trace.failWatchAt = 2
	trace.failUnwatchHandle = 1
	trace.failDestroyHandle = 1
	cleanup, err := watchMetrics([]string{signalPowerDraw, signalTemperature})
	if cleanup != nil || !errors.Is(err, trace.watchError) || !errors.Is(err, trace.unwatchError) || !errors.Is(err, trace.destroyError) {
		t.Fatalf("partial rollback cleanup present = %v, error = %v", cleanup != nil, err)
	}
	if len(trace.groups) != 1 || trace.unwatches[1] != 1 || trace.destroys[1] != 1 || trace.unwatches[2] != 1 || trace.destroys[2] != 1 {
		t.Fatalf("rollback did not attempt each step for every group once: %+v", trace)
	}
	if _, owned := trace.groups[1]; !owned {
		t.Fatal("rollback removed the group whose destruction failed")
	}
}

func TestNativeWatchPolicyCleanupAttemptsEveryStepOnce(t *testing.T) {
	watchMetrics, trace := newPolicyNativeWatchRunner(t, &WatchPolicy{
		DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{signalTemperature: 2 * time.Second},
	})
	trace.failUnwatchHandle = 1
	trace.failDestroyHandle = 1
	cleanup, err := watchMetrics([]string{signalPowerDraw, signalTemperature})
	if err != nil || cleanup == nil {
		t.Fatalf("WatchMetrics() = cleanup present %v, %v", cleanup != nil, err)
	}
	if err := cleanup(); !errors.Is(err, trace.unwatchError) || !errors.Is(err, trace.destroyError) {
		t.Fatalf("cleanup did not preserve both failures: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("repeated cleanup = %v, want nil", err)
	}
	if len(trace.groups) != 1 || trace.unwatches[1] != 1 || trace.destroys[1] != 1 || trace.unwatches[2] != 1 || trace.destroys[2] != 1 {
		t.Fatalf("cleanup did not attempt each step for every group once: %+v", trace)
	}
	if _, owned := trace.groups[1]; !owned {
		t.Fatal("cleanup removed the group whose destruction failed")
	}
}

func TestNativeWatchPolicyUnsupportedOverrideFailsBeforeInit(t *testing.T) {
	source := NewStandaloneSource(StandaloneOptions{WatchPolicy: &WatchPolicy{
		DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{"unsupported.signal": time.Second},
	}})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	library := source.native.(*nativeLibrary)
	initCalls := 0
	library.dcgmInit = func(sessionMode, ...string) (func(), error) {
		initCalls++
		return nil, errors.New("Init should not run")
	}
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "unsupported-policy"), 0, []string{signalPowerDraw})
	failure := values[signalPowerDraw].CollectionError
	if failure == nil || !strings.Contains(failure.Detail, "unsupported.signal") || initCalls != 0 {
		t.Fatalf("unsupported override failure = %v, Init calls = %d", failure, initCalls)
	}
}

func TestNativeWatchPolicyUnsupportedSignalFailsBeforeInit(t *testing.T) {
	source := NewStandaloneSource(StandaloneOptions{})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	library := source.native.(*nativeLibrary)
	initCalls := 0
	library.dcgmInit = func(sessionMode, ...string) (func(), error) {
		initCalls++
		return nil, errors.New("Init should not run")
	}
	if err := source.AddMetricsToWatch([]string{"unsupported.signal"}); err != nil {
		t.Fatal(err)
	}
	values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "unsupported-signal"), 0, []string{"unsupported.signal"})
	failure := values["unsupported.signal"].CollectionError
	if failure == nil || failure.Category != observation.CollectionErrorCategoryUnsupported || !strings.Contains(failure.Detail, "unsupported.signal") || initCalls != 0 {
		t.Fatalf("unsupported signal failure = %v, Init calls = %d", failure, initCalls)
	}
}

func TestNativeWatchPolicyUnsupportedOverrideFailsBeforeInventoryInit(t *testing.T) {
	source := NewStandaloneSource(StandaloneOptions{WatchPolicy: &WatchPolicy{
		DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{"unsupported.signal": time.Second},
	}})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	library := source.native.(*nativeLibrary)
	initCalls := 0
	library.dcgmInit = func(sessionMode, ...string) (func(), error) {
		initCalls++
		return nil, errors.New("Init should not run")
	}
	snapshot := source.GPUInventory(context.Background(), newTestCycle(t, "unsupported-inventory-policy"), []GPUIdentityEntry{{Index: 0}})
	entries := snapshot.GetInventory().GetEntries()
	if len(entries) != 1 {
		t.Fatalf("inventory entries = %v, want one failure", entries)
	}
	failure := entries[0].CollectionError
	if failure == nil || !strings.Contains(failure.Detail, "unsupported.signal") || initCalls != 0 {
		t.Fatalf("unsupported override failure = %v, Init calls = %d", failure, initCalls)
	}
}

func TestNativeWatchPolicyConstructorsFreezeCallerPolicy(t *testing.T) {
	for _, test := range []struct {
		create func(*WatchPolicy) *Source
		name   string
	}{
		{name: "standalone", create: func(policy *WatchPolicy) *Source { return NewStandaloneSource(StandaloneOptions{WatchPolicy: policy}) }},
		{name: "embedded", create: func(policy *WatchPolicy) *Source { return NewEmbeddedSource(EmbeddedOptions{WatchPolicy: policy}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := &WatchPolicy{DefaultInterval: 30 * time.Second, SignalIntervals: map[string]time.Duration{signalPowerDraw: time.Second}}
			source := test.create(policy)
			policy.DefaultInterval = 0
			policy.SignalIntervals[signalPowerDraw] = 0
			library := source.native.(*nativeLibrary)
			groups, err := library.MetricWatchGroups([]string{signalPowerDraw})
			if err != nil {
				t.Fatalf("caller mutation changed frozen policy: %v", err)
			}
			if len(groups) != 1 || groups[0].interval != time.Second {
				t.Fatalf("frozen native groups = %+v", groups)
			}
		})
	}
}

func TestCurrentDCGMFieldsFitOneLatestValueRead(t *testing.T) {
	signalIDs := make([]string, 0, len(fieldsBySignal))
	for signalID := range fieldsBySignal {
		signalIDs = append(signalIDs, signalID)
	}
	mappings, err := fieldMappingsFor(signalIDs)
	if err != nil {
		t.Fatal(err)
	}
	fieldIDs := uniqueFieldIDs(mappings)
	if len(fieldIDs) == 0 || len(fieldIDs) > 127 {
		t.Fatalf("current unique DCGM fields = %d; reconsider the latest-value read path", len(fieldIDs))
	}
	t.Logf("%d canonical signals resolve to %d native fields, within one conservative 127-field read", len(signalIDs), len(fieldIDs))
}
