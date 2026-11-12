// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestInvalidWatchPolicyFailsBeforeNativeSetup(t *testing.T) {
	for _, constructor := range []struct {
		newSource func(*WatchPolicy) *Source
		name      string
	}{
		{name: "standalone", newSource: func(policy *WatchPolicy) *Source { return NewStandaloneSource(StandaloneOptions{WatchPolicy: policy}) }},
		{name: "embedded", newSource: func(policy *WatchPolicy) *Source { return NewEmbeddedSource(EmbeddedOptions{WatchPolicy: policy}) }},
	} {
		t.Run(constructor.name, func(t *testing.T) {
			for _, policy := range []*WatchPolicy{
				{},
				{DefaultInterval: -time.Second},
				{DefaultInterval: 1500 * time.Nanosecond},
				{DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{signalPowerDraw: 1500 * time.Nanosecond}},
			} {
				source := constructor.newSource(policy)
				native := &fakeNative{}
				source.native = native
				source.nativeCallMu = &sync.Mutex{}
				if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err == nil {
					t.Fatal("invalid policy accepted")
				}
				if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "invalid-policy")); failure == nil {
					t.Fatal("invalid policy allowed collection")
				}
				if native.initCalls.Load() != 0 || native.enumerationCalls.Load() != 0 || native.watchCalls.Load() != 0 {
					t.Fatal("invalid policy entered native setup")
				}
			}
		})
	}
}

type preflightNative struct {
	failure error
	fakeNative
}

func (native *preflightNative) MetricWatchGroups([]string) ([]metricWatchGroup, error) {
	return nil, native.failure
}

func TestWatchPreflightPrecedesClientInit(t *testing.T) {
	failure := errors.New("watch configuration rejected")
	native := &preflightNative{failure: failure}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	if _, result := source.GPUIdentities(context.Background(), newTestCycle(t, "preflight")); result == nil || result.GetDetail() != failure.Error() {
		t.Fatalf("preflight result: %v", result)
	}
	if native.initCalls.Load() != 0 || native.enumerationCalls.Load() != 0 {
		t.Fatal("preflight happened after native setup")
	}
}

type watchPreparationNative struct {
	groups          []metricWatchGroup
	preparedSignals [][]string
	watchedGroups   [][]metricWatchGroup
	calls           []string
	fakeNative
}

func (native *watchPreparationNative) MetricWatchGroups(signalIDs []string) ([]metricWatchGroup, error) {
	native.calls = append(native.calls, "prepare")
	native.preparedSignals = append(native.preparedSignals, slices.Clone(signalIDs))
	if len(signalIDs) == 0 {
		return []metricWatchGroup{}, nil
	}
	return native.groups, nil
}

func (native *watchPreparationNative) Init() (func(), error) {
	native.calls = append(native.calls, "init")
	return native.fakeNative.Init()
}

func (native *watchPreparationNative) WatchMetrics(groups []metricWatchGroup) (func() error, error) {
	native.calls = append(native.calls, "watch")
	native.watchedGroups = append(native.watchedGroups, groups)
	return native.fakeNative.WatchMetrics(groups)
}

func TestMetricWatchPreparationPrecedesInitAndIsReused(t *testing.T) {
	groups := []metricWatchGroup{{fieldIDs: []uint16{11, 12}, interval: time.Second}}
	native := &watchPreparationNative{groups: groups}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "prepare-before-init")); failure != nil {
		t.Fatal(failure)
	}
	if !slices.Equal(native.calls, []string{"prepare", "init"}) {
		t.Fatalf("setup calls = %v, want preparation before Init and no watch", native.calls)
	}

	for _, cycleID := range []string{"first-metric-read", "second-metric-read"} {
		values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, cycleID), 0, []string{signalPowerDraw})
		if values[signalPowerDraw].Value == nil {
			t.Fatalf("ReadGPUMetrics() = %+v", values)
		}
	}
	if !slices.Equal(native.calls, []string{"prepare", "init", "watch"}) {
		t.Fatalf("setup calls across reads = %v, want one preparation, Init, and watch", native.calls)
	}
	if !reflect.DeepEqual(native.watchedGroups, [][]metricWatchGroup{groups}) {
		t.Fatalf("watched groups = %+v, want prepared groups %+v", native.watchedGroups, groups)
	}
	if got := native.readCalls.Load(); got != 2 {
		t.Fatalf("metric reads = %d, want 2", got)
	}
}

func TestMetricWatchPreparationSurvivesInitRetries(t *testing.T) {
	groups := []metricWatchGroup{{fieldIDs: []uint16{11}, interval: time.Second}}
	native := &watchPreparationNative{groups: groups, fakeNative: fakeNative{initErr: errNoHostengine}}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "first-init-failure")); failure == nil {
		t.Fatal("GPUIdentities() succeeded despite failed Init")
	}
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatalf("duplicate registration after failed Init: %v", err)
	}
	native.groups = []metricWatchGroup{{fieldIDs: []uint16{99}, interval: 2 * time.Second}}
	if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "second-init-failure")); failure == nil {
		t.Fatal("GPUIdentities() retry succeeded despite failed Init")
	}
	native.initErr = nil
	values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "successful-init-retry"), 0, []string{signalPowerDraw})
	if values[signalPowerDraw].Value == nil {
		t.Fatalf("ReadGPUMetrics() = %+v", values)
	}
	if !slices.Equal(native.calls, []string{"prepare", "init", "init", "init", "watch"}) {
		t.Fatalf("retry setup calls = %v, want preparation reused across Init attempts", native.calls)
	}
	if !reflect.DeepEqual(native.watchedGroups, [][]metricWatchGroup{groups}) {
		t.Fatalf("watched groups = %+v, want original prepared groups %+v", native.watchedGroups, groups)
	}
}

func TestAddingMetricAfterFailedInitRepreparesWatch(t *testing.T) {
	for _, test := range []struct {
		name           string
		initialSignals []string
	}{
		{name: "inventory only"},
		{name: "existing metric", initialSignals: []string{signalPowerDraw}},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := &watchPreparationNative{
				groups:     []metricWatchGroup{{fieldIDs: []uint16{11}, interval: time.Second}},
				fakeNative: fakeNative{initErr: errNoHostengine},
			}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() {
				if err := source.Close(); err != nil {
					t.Error(err)
				}
			})
			if len(test.initialSignals) != 0 {
				if err := source.AddMetricsToWatch(test.initialSignals); err != nil {
					t.Fatal(err)
				}
			}
			if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "failed-inventory-init")); failure == nil {
				t.Fatal("GPUIdentities() succeeded despite failed Init")
			}
			signalIDs := []string{signalPowerDraw, signalTemperature}
			if err := source.AddMetricsToWatch(signalIDs); err != nil {
				t.Fatalf("new registration after failed Init: %v", err)
			}
			groups := []metricWatchGroup{
				{fieldIDs: []uint16{21}, interval: time.Second},
				{fieldIDs: []uint16{22}, interval: 2 * time.Second},
			}
			native.groups = groups
			native.initErr = nil
			values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "updated-metric-watch"), 0, signalIDs)
			for _, signalID := range signalIDs {
				if values[signalID].Value == nil {
					t.Fatalf("ReadGPUMetrics() for %s = %+v", signalID, values[signalID])
				}
			}
			if !reflect.DeepEqual(native.preparedSignals, [][]string{test.initialSignals, signalIDs}) {
				t.Fatalf("prepared signals = %v, want new preparation for %v", native.preparedSignals, signalIDs)
			}
			if !slices.Equal(native.calls, []string{"prepare", "init", "prepare", "init", "watch"}) {
				t.Fatalf("setup calls after registration = %v, want new preparation before retrying Init", native.calls)
			}
			if !reflect.DeepEqual(native.watchedGroups, [][]metricWatchGroup{groups}) {
				t.Fatalf("watched groups = %+v, want updated prepared groups %+v", native.watchedGroups, groups)
			}
		})
	}
}

func TestInventoryWithoutMetricsReusesPreparationAndDoesNotWatch(t *testing.T) {
	native := &watchPreparationNative{fakeNative: fakeNative{initErr: errNoHostengine}}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, cycleID := range []string{"first-empty-init-failure", "second-empty-init-failure"} {
		if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, cycleID)); failure == nil {
			t.Fatal("GPUIdentities() succeeded despite failed Init")
		}
	}
	native.initErr = nil
	for _, cycleID := range []string{"first-inventory", "second-inventory"} {
		cycle := newTestCycle(t, cycleID)
		identities, failure := source.GPUIdentities(context.Background(), cycle)
		if failure != nil {
			t.Fatal(failure)
		}
		if snapshot := source.GPUInventory(context.Background(), cycle, identities); len(snapshot.GetInventory().GetEntries()) != 1 {
			t.Fatalf("GPUInventory() = %+v", snapshot)
		}
	}
	if !slices.Equal(native.calls, []string{"prepare", "init", "init", "init"}) {
		t.Fatalf("inventory setup calls = %v, want preparation reused without a watch", native.calls)
	}
	if len(native.preparedSignals) != 1 || len(native.preparedSignals[0]) != 0 {
		t.Fatalf("prepared signals = %v, want one preparation with no metrics", native.preparedSignals)
	}
	if got := native.inventoryCalls.Load(); got != 2 {
		t.Fatalf("inventory reads = %d, want 2", got)
	}
	if got := native.watchCalls.Load(); got != 0 {
		t.Fatalf("watch calls = %d, want 0", got)
	}
}
