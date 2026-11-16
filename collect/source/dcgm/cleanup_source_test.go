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
	"time"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

type cleanupNative struct {
	setupError      error
	cleanupError    error
	nativeCallGate  *sync.Mutex
	rollbackStarted chan struct{}
	releaseRollback chan struct{}
	fakeNative
	cleanupOutsideGate atomic.Bool
	missingCleanup     bool
}

func (native *cleanupNative) recordCleanupGate() {
	if native.nativeCallGate != nil && native.nativeCallGate.TryLock() {
		native.cleanupOutsideGate.Store(true)
		native.nativeCallGate.Unlock()
	}
}

func (native *cleanupNative) Init() (func(), error) {
	cleanup, err := native.fakeNative.Init()
	if err != nil {
		return nil, err
	}
	return func() {
		native.recordCleanupGate()
		cleanup()
	}, nil
}

func (native *cleanupNative) WatchMetrics(groups []metricWatchGroup) (func() error, error) {
	if native.missingCleanup {
		native.watchCalls.Add(1)
		return nil, nil
	}
	watchCleanup, _ := native.fakeNative.WatchMetrics(groups)
	cleanup := sync.OnceValue(func() error {
		native.recordCleanupGate()
		_ = watchCleanup()
		return native.cleanupError
	})
	if native.setupError != nil {
		if native.rollbackStarted != nil {
			close(native.rollbackStarted)
			<-native.releaseRollback
		}
		// Native setup owns its rollback. An error never transfers a cleanup
		// function back to Source, even if that one rollback pass fails.
		return nil, errors.Join(native.setupError, cleanup())
	}
	return cleanup, nil
}

func TestCloseReleasesClientAfterPersistentWatchCleanupFailure(t *testing.T) {
	unwatchError, destroyError := errors.New("unwatch failed"), errors.New("destroy failed")
	gate := &sync.Mutex{}
	native := &cleanupNative{cleanupError: errors.Join(unwatchError, destroyError), nativeCallGate: gate}
	source := newSource(native, gate)
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "setup"), 0, []string{signalPowerDraw})
	if failure := values[signalPowerDraw].CollectionError; failure != nil {
		t.Fatal(failure)
	}
	if err := source.Close(); !errors.Is(err, unwatchError) || !errors.Is(err, destroyError) {
		t.Fatalf("Close() lost cleanup causes: %v", err)
	}
	if native.watchCleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 || native.watchCleanedAfterClient.Load() || native.cleanupOutsideGate.Load() {
		t.Fatalf("cleanup calls watch=%d client=%d, watch after client=%t, outside gate=%t",
			native.watchCleanupCalls.Load(), native.cleanupCalls.Load(), native.watchCleanedAfterClient.Load(), native.cleanupOutsideGate.Load())
	}
	for range 2 {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() retried a completed best-effort cleanup: %v", err)
		}
	}
	values = source.ReadGPUMetrics(context.Background(), newTestCycle(t, "closed"), 0, []string{signalPowerDraw})
	if values[signalPowerDraw].CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable || native.readCalls.Load() != 1 {
		t.Fatal("closed source entered native metric read")
	}
	if native.watchCleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 {
		t.Fatal("later Close repeated consumed cleanup ownership")
	}
}

func TestFailedMetricWatchPermanentlyClosesSource(t *testing.T) {
	setupError, rollbackError := errors.New("watch setup failed"), errors.New("watch rollback failed")
	for _, test := range []struct {
		setupError     error
		cleanupError   error
		name           string
		wantDetails    []string
		wantRollbacks  int32
		missingCleanup bool
	}{
		{name: "setup failure", setupError: setupError, wantDetails: []string{setupError.Error()}, wantRollbacks: 1},
		{name: "setup and rollback failure", setupError: setupError, cleanupError: rollbackError, wantDetails: []string{setupError.Error(), rollbackError.Error()}, wantRollbacks: 1},
		{name: "missing cleanup", missingCleanup: true, wantDetails: []string{"no cleanup function"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			gate := &sync.Mutex{}
			native := &cleanupNative{
				setupError: test.setupError, cleanupError: test.cleanupError,
				nativeCallGate: gate, missingCleanup: test.missingCleanup,
			}
			source := newSource(native, gate)
			if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
				t.Fatal(err)
			}
			values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "failed-setup"), 0, []string{signalPowerDraw})
			failure := values[signalPowerDraw].CollectionError
			if failure == nil {
				t.Fatal("failed watch setup produced no collection error")
			}
			for _, detail := range test.wantDetails {
				if !strings.Contains(failure.GetDetail(), detail) {
					t.Fatalf("setup failure %v lost detail %q", failure, detail)
				}
			}
			if native.cleanupCalls.Load() != 1 || native.watchCleanupCalls.Load() != test.wantRollbacks || native.watchCleanedAfterClient.Load() || native.cleanupOutsideGate.Load() {
				t.Fatalf("failed setup cleanup watch=%d client=%d, watch after client=%t, outside gate=%t",
					native.watchCleanupCalls.Load(), native.cleanupCalls.Load(), native.watchCleanedAfterClient.Load(), native.cleanupOutsideGate.Load())
			}

			// Repairing the backend does not make this Source reusable. A caller
			// must construct another one after a watch setup failure.
			native.setupError = nil
			native.cleanupError = nil
			native.missingCleanup = false
			for _, cycleID := range []string{"same-source-retry", "later-cycle-retry"} {
				cycle := newTestCycle(t, cycleID)
				values = source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
				if values[signalPowerDraw].CollectionError.GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatalf("metric retry after failed setup = %v", values[signalPowerDraw].CollectionError)
				}
				if _, identityFailure := source.GPUIdentities(context.Background(), cycle); identityFailure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatalf("identity read after failed setup = %v", identityFailure)
				}
				snapshot := source.GPUInventory(context.Background(), cycle, []GPUIdentityEntry{{Index: 0, UUID: "GPU-0"}})
				entries := snapshot.GetInventory().GetEntries()
				if len(entries) != 1 || entries[0].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatalf("inventory read after failed setup = %v", snapshot)
				}
			}
			if err := source.AddMetricsToWatch([]string{signalTemperature}); err == nil {
				t.Fatal("registration succeeded on a source closed by setup failure")
			}
			for range 2 {
				if err := source.Close(); err != nil {
					t.Fatalf("Close() repeated failed setup rollback: %v", err)
				}
			}
			if native.initCalls.Load() != 1 || native.watchCalls.Load() != 1 || native.readCalls.Load() != 0 || native.enumerationCalls.Load() != 0 || native.uuidCalls.Load() != 0 || native.inventoryCalls.Load() != 0 {
				t.Fatal("failed setup allowed more native allocation or collection")
			}
			if native.watchCleanupCalls.Load() != test.wantRollbacks || native.cleanupCalls.Load() != 1 {
				t.Fatal("failed setup cleanup repeated")
			}
		})
	}
}

type clientReferenceNative struct {
	clientReferences *atomic.Int32
	cleanupNative
}

func (native *clientReferenceNative) Init() (func(), error) {
	cleanup, err := native.cleanupNative.Init()
	if err != nil {
		return nil, err
	}
	native.clientReferences.Add(1)
	return func() {
		cleanup()
		native.clientReferences.Add(-1)
	}, nil
}

func TestFailedSourceDoesNotRetainReplacementClientReference(t *testing.T) {
	gate := &sync.Mutex{}
	clientReferences := &atomic.Int32{}
	failedNative := &clientReferenceNative{
		clientReferences: clientReferences,
		cleanupNative: cleanupNative{
			setupError: errors.New("setup failed"), cleanupError: errors.New("rollback failed"), nativeCallGate: gate,
		},
	}
	failed := newSource(failedNative, gate)
	if err := failed.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	values := failed.ReadGPUMetrics(context.Background(), newTestCycle(t, "failed-source"), 0, []string{signalPowerDraw})
	if values[signalPowerDraw].CollectionError == nil || clientReferences.Load() != 0 {
		t.Fatalf("failed watch retained a client reference: refs=%d, error=%v", clientReferences.Load(), values[signalPowerDraw].CollectionError)
	}
	replacementNative := &clientReferenceNative{
		clientReferences: clientReferences, cleanupNative: cleanupNative{nativeCallGate: gate},
	}
	replacement := newSource(replacementNative, gate)
	t.Cleanup(func() {
		if err := replacement.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := replacement.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	values = replacement.ReadGPUMetrics(context.Background(), newTestCycle(t, "replacement-source"), 0, []string{signalPowerDraw})
	if values[signalPowerDraw].Value == nil || clientReferences.Load() != 1 {
		t.Fatalf("replacement read = %v, client references = %d", values[signalPowerDraw].CollectionError, clientReferences.Load())
	}
	for range 2 {
		if err := failed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	values = replacement.ReadGPUMetrics(context.Background(), newTestCycle(t, "replacement-after-old-close"), 0, []string{signalPowerDraw})
	if values[signalPowerDraw].Value == nil || clientReferences.Load() != 1 || failedNative.cleanupCalls.Load() != 1 {
		t.Fatalf("old source disturbed replacement: refs=%d, old cleanups=%d, error=%v", clientReferences.Load(), failedNative.cleanupCalls.Load(), values[signalPowerDraw].CollectionError)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if clientReferences.Load() != 0 || replacementNative.cleanupCalls.Load() != 1 {
		t.Fatalf("replacement client references=%d, cleanups=%d", clientReferences.Load(), replacementNative.cleanupCalls.Load())
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

func TestCloseDuringFailedWatchSetupReleasesClient(t *testing.T) {
	gate := &sync.Mutex{}
	native := &cleanupNative{
		setupError: errors.New("setup failed"), cleanupError: errors.New("rollback failed"), nativeCallGate: gate,
		rollbackStarted: make(chan struct{}), releaseRollback: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(native.releaseRollback) })
	t.Cleanup(release)
	source := newSource(native, gate)
	if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan map[string]FieldValue, 1)
	cycle := newTestCycle(t, "failed-watch-rollback")
	go func() { finished <- source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw}) }()
	select {
	case <-native.rollbackStarted:
	case <-time.After(time.Second):
		t.Fatal("native rollback did not start")
	}
	if err := source.Close(); !errors.Is(err, ErrInUse) {
		t.Fatalf("busy Close: %v", err)
	}
	if native.cleanupCalls.Load() != 0 {
		t.Fatal("busy Close released client while native rollback was running")
	}
	release()
	select {
	case values := <-finished:
		failure := values[signalPowerDraw].CollectionError
		if !strings.Contains(failure.GetDetail(), native.setupError.Error()) || !strings.Contains(failure.GetDetail(), native.cleanupError.Error()) {
			t.Fatalf("failed setup result = %v", failure)
		}
	case <-time.After(time.Second):
		t.Fatal("failed watch setup did not finish")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if native.initCalls.Load() != 1 || native.watchCalls.Load() != 1 || native.readCalls.Load() != 0 || native.watchCleanupCalls.Load() != 1 || native.cleanupCalls.Load() != 1 || native.cleanupOutsideGate.Load() {
		t.Fatalf("failed setup calls init=%d watch=%d read=%d watch cleanup=%d client cleanup=%d outside gate=%t",
			native.initCalls.Load(), native.watchCalls.Load(), native.readCalls.Load(), native.watchCleanupCalls.Load(), native.cleanupCalls.Load(), native.cleanupOutsideGate.Load())
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
