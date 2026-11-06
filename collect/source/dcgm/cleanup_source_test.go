// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

type cleanupNative struct {
	setupError   error
	cleanupError error
	fakeNative
}

func (native *cleanupNative) WatchMetrics([]metricWatchGroup) (func() error, error) {
	native.watchCalls.Add(1)
	return func() error {
		native.watchCleanupCalls.Add(1)
		if native.cleanupError != nil {
			err := native.cleanupError
			native.cleanupError = nil
			return err
		}
		return nil
	}, native.setupError
}

func TestCloseRetainsUnfinishedCleanup(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "active watch", true: "partial setup"}[partial], func(t *testing.T) {
			native := &cleanupNative{cleanupError: errors.New("cleanup failed")}
			if partial {
				native.setupError = errors.New("setup failed")
			}
			source := newSource(native, &sync.Mutex{})
			if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
				t.Fatal(err)
			}
			values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "setup"), 0, []string{signalPowerDraw})
			if (values[signalPowerDraw].CollectionError != nil) != partial {
				t.Fatalf("setup result: %v", values)
			}
			if source.watchActive == partial {
				t.Fatal("failed setup was treated as active")
			}
			if err := source.Close(); err == nil {
				t.Fatal("first Close succeeded")
			}
			if native.cleanupCalls.Load() != 0 {
				t.Fatal("released client before resources")
			}
			if source.metricWatchCleanup == nil {
				t.Fatal("lost pending cleanup")
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if native.watchCleanupCalls.Load() != 2 || native.cleanupCalls.Load() != 1 {
				t.Fatalf("cleanup calls watch=%d client=%d", native.watchCleanupCalls.Load(), native.cleanupCalls.Load())
			}
			if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "closed")); failure == nil {
				t.Fatal("closed source entered native API")
			}
		})
	}
}

func TestPartialSetupRollbackBeforeRetry(t *testing.T) {
	native := &cleanupNative{setupError: errors.New("setup failed"), cleanupError: errors.New("rollback failed")}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]FieldValue {
		return source.ReadGPUMetrics(context.Background(), newTestCycle(t, "retry"), 0, []string{signalPowerDraw})
	}
	if read()[signalPowerDraw].CollectionError == nil {
		t.Fatal("setup succeeded")
	}
	if read()[signalPowerDraw].CollectionError == nil || native.watchCalls.Load() != 1 {
		t.Fatal("created watch before rollback finished")
	}
	native.setupError = nil
	if failure := read()[signalPowerDraw].CollectionError; failure != nil {
		t.Fatal(failure)
	}
	if native.watchCalls.Load() != 2 || native.watchCleanupCalls.Load() != 2 {
		t.Fatal("setup retry did not finish rollback first")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBusyClosePreventsFurtherNativeWork(t *testing.T) {
	native := &fakeNative{readStarted: make(chan struct{}), releaseRead: make(chan struct{})}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan map[string]FieldValue, 1)
	go func() {
		finished <- source.ReadGPUMetrics(context.Background(), newTestCycle(t, "active"), 0, []string{signalPowerDraw})
	}()
	<-native.readStarted
	if err := source.Close(); !errors.Is(err, ErrInUse) {
		t.Fatalf("busy Close: %v", err)
	}
	close(native.releaseRead)
	<-finished
	values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "after close request"), 0, []string{signalPowerDraw})
	if values[signalPowerDraw].CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable || native.readCalls.Load() != 1 {
		t.Fatal("native work entered after close request")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if native.watchCleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
		t.Fatal("busy close lost resources")
	}
}

func TestCloseDuringRollbackPreventsWatchSetup(t *testing.T) {
	native := &fakeNative{}
	source := newSource(native, &sync.Mutex{})
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	source.metricWatchCleanup = func() error {
		close(started)
		<-release
		return nil
	}
	finished := make(chan map[string]FieldValue, 1)
	cycle := newTestCycle(t, "rollback")
	go func() { finished <- source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw}) }()
	<-started
	if err := source.Close(); !errors.Is(err, ErrInUse) {
		t.Fatalf("busy Close: %v", err)
	}
	close(release)
	values := <-finished
	if values[signalPowerDraw].CollectionError == nil || native.watchCalls.Load() != 0 || native.readCalls.Load() != 0 {
		t.Fatal("new native work entered after close during rollback")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
}

type blockingSetupNative struct {
	started chan struct{}
	release chan struct{}
	fakeNative
	blockWatch bool
}

func (native *blockingSetupNative) Init() (func(), error) {
	if !native.blockWatch {
		close(native.started)
		<-native.release
	}
	return native.fakeNative.Init()
}

func (native *blockingSetupNative) WatchMetrics(groups []metricWatchGroup) (func() error, error) {
	if native.blockWatch {
		close(native.started)
		<-native.release
	}
	return native.fakeNative.WatchMetrics(groups)
}

func TestCloseDuringSetupPreventsMetricRead(t *testing.T) {
	for _, stage := range []string{"init", "watch"} {
		t.Run(stage, func(t *testing.T) {
			native := &blockingSetupNative{
				started: make(chan struct{}), release: make(chan struct{}),
				blockWatch: stage == "watch",
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(native.release) }) }
			t.Cleanup(release)
			source := newSource(native, &sync.Mutex{})
			if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
				t.Fatal(err)
			}
			cycle := newTestCycle(t, "close-during-"+stage)
			finished := make(chan map[string]FieldValue, 1)
			go func() {
				finished <- source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
			}()
			select {
			case <-native.started:
			case <-time.After(time.Second):
				t.Fatal("native setup did not start")
			}
			if err := source.Close(); !errors.Is(err, ErrInUse) {
				t.Fatalf("busy Close: %v", err)
			}
			release()
			select {
			case values := <-finished:
				if values[signalPowerDraw].CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatalf("setup continued after close request: %v", values)
				}
			case <-time.After(time.Second):
				t.Fatal("native setup did not finish")
			}
			var expectedWatches int32
			if native.blockWatch {
				expectedWatches = 1
			}
			if native.initCalls.Load() != 1 || native.watchCalls.Load() != expectedWatches || native.readCalls.Load() != 0 {
				t.Fatalf("setup calls init=%d watch=%d read=%d", native.initCalls.Load(), native.watchCalls.Load(), native.readCalls.Load())
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if native.watchCleanupCalls.Load() != expectedWatches || native.cleanupCalls.Load() != 1 {
				t.Fatalf("cleanup calls watch=%d client=%d", native.watchCleanupCalls.Load(), native.cleanupCalls.Load())
			}
		})
	}
}
