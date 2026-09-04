// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
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
	readStarted chan struct{}
	releaseRead chan struct{}

	cleanupCalls      atomic.Int32
	initCalls         atomic.Int32
	readCalls         atomic.Int32
	watchCleanupCalls atomic.Int32
	watchCalls        atomic.Int32
}

func (native *fakeNative) Init() (func(), error) {
	native.initCalls.Add(1)
	return func() { native.cleanupCalls.Add(1) }, nil
}

func (*fakeNative) SupportedDevices() ([]uint, error) { return []uint{0}, nil }
func (*fakeNative) DeviceUUID(uint) (string, error)   { return "GPU-0", nil }

func (native *fakeNative) WatchMetrics([]string) (func() error, error) {
	native.watchCalls.Add(1)
	return func() error {
		native.watchCleanupCalls.Add(1)
		return nil
	}, nil
}

func (native *fakeNative) ReadMetrics(_ uint, signalIDs []string) (map[string]FieldValue, error) {
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

func TestReadMetricsFreezesWatchAndCloseReleasesResources(t *testing.T) {
	native := &fakeNative{}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatalf("AddMetricsToWatch() error = %v", err)
	}

	values := source.ReadMetrics(
		context.Background(),
		newTestCycle(t, "first-read"),
		Device{Index: 0, UUID: "GPU-0"},
		[]string{signalPowerDraw},
	)
	if failure := values[signalPowerDraw].CollectionError; failure != nil {
		t.Fatalf("ReadMetrics() collection error = %v", failure)
	}
	if err := source.AddMetricsToWatch([]string{signalTemperature}); err == nil {
		t.Fatal("AddMetricsToWatch() after ReadMetrics() returned nil error")
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
		t.Errorf("ReadMetrics() calls = %d, want 1", got)
	}
	if got := native.watchCleanupCalls.Load(); got != 1 {
		t.Errorf("watch cleanup calls = %d, want 1", got)
	}
	if got := native.cleanupCalls.Load(); got != 1 {
		t.Errorf("client cleanup calls = %d, want 1", got)
	}
}

func TestReadMetricsRejectsConcurrentNativeCall(t *testing.T) {
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
		firstRead <- source.ReadMetrics(
			context.Background(),
			firstCycle,
			Device{Index: 0, UUID: "GPU-0"},
			[]string{signalPowerDraw},
		)
	}()

	select {
	case <-native.readStarted:
	case <-time.After(time.Second):
		t.Fatal("first native read did not start")
	}

	second := source.ReadMetrics(
		context.Background(),
		secondCycle,
		Device{Index: 0, UUID: "GPU-0"},
		[]string{signalPowerDraw},
	)
	failure := second[signalPowerDraw].CollectionError
	if failure == nil {
		t.Fatal("concurrent ReadMetrics() returned no collection error")
	}
	if failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
		t.Errorf("category = %v, want %v", failure.GetCategory(), observation.CollectionErrorCategoryUnavailable)
	}
	if got := native.readCalls.Load(); got != 1 {
		t.Errorf("native ReadMetrics() calls = %d, want 1", got)
	}

	releaseNativeRead()
	select {
	case values := <-firstRead:
		if failure := values[signalPowerDraw].CollectionError; failure != nil {
			t.Errorf("first ReadMetrics() collection error = %v", failure)
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
