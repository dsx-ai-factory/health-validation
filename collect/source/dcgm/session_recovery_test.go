// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

type recoveryNative struct {
	releaseError error
	metricError  error
	metricValues map[string]FieldValue
	*healthLifecycleNative
}

func (native *recoveryNative) Init() (func() error, error) {
	cleanup, err := native.healthLifecycleNative.Init()
	if err != nil {
		return nil, err
	}
	releaseError := native.releaseError
	return func() error { return errors.Join(cleanup(), releaseError) }, nil
}

func (native *recoveryNative) ReadGPUMetrics(gpu uint, signals []string) (map[string]FieldValue, error) {
	if native.metricValues != nil || native.metricError != nil {
		native.readCalls.Add(1)
		return native.metricValues, native.metricError
	}
	return native.healthLifecycleNative.ReadGPUMetrics(gpu, signals)
}

func connectionLoss() error {
	return &dcgmError{
		sourceError: connectionNotValidStatus,
		category:    observation.CollectionErrorCategoryUnavailable, detail: "native connection lost",
	}
}

func TestInitializationFailureSuppressesNativeWorkForCycle(t *testing.T) {
	for _, initFailure := range []error{errors.New("native Init is not ready"), connectionLoss()} {
		for _, firstOperation := range []string{"enumeration", "metrics", "health"} {
			t.Run(firstOperation+"/"+initFailure.Error(), func(t *testing.T) {
				gate := &sync.Mutex{}
				native := &healthLifecycleNative{cleanupNative: cleanupNative{nativeCallGate: gate}}
				native.initErr = initFailure
				source := newSource(native, gate)
				t.Cleanup(func() { _ = source.Close() })
				registerMetricAndHealth(t, source)
				cycle := newTestCycle(t, "initialization-retry")
				wantFailure := collectionError(initFailure)
				checkFailure := func(operation string, failure *observation.CollectionError) {
					t.Helper()
					if failure == nil || failure.GetSourceError() != wantFailure.GetSourceError() || failure.GetCategory() != wantFailure.GetCategory() || failure.GetDetail() != wantFailure.GetDetail() {
						t.Fatalf("%s lost the initial failure: %v, want %v", operation, failure, &wantFailure)
					}
				}
				switch firstOperation {
				case "enumeration":
					_, failure := source.newCollectionReader(cycle).gpuIndices(context.Background())
					checkFailure("initial enumeration", failure)
				case "metrics":
					values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
					checkFailure("initial metrics", values[signalPowerDraw].CollectionError)
				case "health":
					_, failure := source.newCollectionReader(cycle).readHealth(context.Background())
					checkFailure("initial health", failure)
				}
				native.initErr = nil
				values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
				checkFailure("same-cycle metrics", values[signalPowerDraw].CollectionError)
				_, failure := source.newCollectionReader(cycle).readHealth(context.Background())
				checkFailure("same-cycle health", failure)
				identities := source.newCollectionReader(cycle).gpuUUIDs(context.Background(), []uint{0})
				if len(identities) != 1 || identities[0].Index != 0 || identities[0].UUID != "" {
					t.Fatalf("same-cycle UUID failure lost the requested GPU: %v", identities)
				}
				checkFailure("same-cycle UUID", identities[0].UUIDError)
				snapshot := source.GPUInventory(context.Background(), cycle, []GPUIdentityEntry{{Index: 0, UUID: "GPU-0"}})
				entries := snapshot.GetInventory().GetEntries()
				if len(entries) != 1 || entries[0].GetGpu().GetUuid() != "GPU-0" {
					t.Fatalf("same-cycle inventory failure lost known identity: %v", snapshot)
				}
				checkFailure("same-cycle inventory", entries[0].GetCollectionError())
				_, failure = source.newCollectionReader(cycle).gpuIndices(context.Background())
				checkFailure("same-cycle enumeration", failure)
				if native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 || native.watchCalls.Load() != 0 || native.healthWatchCalls.Load() != 0 || native.readCalls.Load() != 0 || native.uuidCalls.Load() != 0 || native.inventoryCalls.Load() != 0 || native.enumerationCalls.Load() != 0 {
					t.Fatal("initialization failure allowed more native work in its cycle")
				}
				if source.closed.Load() || source.session != nil {
					t.Fatal("failed initialization closed the Source or retained a nonexistent session")
				}

				// A new cycle may reuse the consumer's collection ID.
				next := newTestCycle(t, "initialization-retry")
				values = source.ReadGPUMetrics(context.Background(), next, 0, []string{signalPowerDraw})
				if values[signalPowerDraw].Value == nil {
					t.Fatalf("next-cycle metric setup did not retry: %v", values)
				}
				if report, failure := source.newCollectionReader(next).readHealth(context.Background()); failure != nil || report == nil {
					t.Fatalf("next-cycle health did not recover: %v, %v", report, failure)
				}
				identities = source.newCollectionReader(next).gpuUUIDs(context.Background(), []uint{0})
				if len(identities) != 1 || identities[0].UUID != "GPU-0" || identities[0].UUIDError != nil {
					t.Fatalf("next-cycle UUID did not recover: %v", identities)
				}
				snapshot = source.GPUInventory(context.Background(), next, identities)
				if entries := snapshot.GetInventory().GetEntries(); len(entries) != 1 || entries[0].GetCollectionError() != nil {
					t.Fatalf("next-cycle inventory did not recover: %v", snapshot)
				}
				if indices, failure := source.newCollectionReader(next).gpuIndices(context.Background()); failure != nil || len(indices) != 1 {
					t.Fatalf("next-cycle enumeration did not recover: %v, %v", indices, failure)
				}
				if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 0 || native.watchCalls.Load() != 1 || native.healthWatchCalls.Load() != 1 || native.readCalls.Load() != 1 || native.uuidCalls.Load() != 1 || native.inventoryCalls.Load() != 1 || native.enumerationCalls.Load() != 1 {
					t.Fatal("next cycle did not initialize once and collect every requested operation")
				}
				if err := source.Close(); err != nil || native.cleanupCalls.Load() != 1 {
					t.Fatalf("failed initialization retained cleanup ownership: %v", err)
				}
			})
		}
	}
}

func TestInventoryRecoveryKeepsWatchRegistrationOpen(t *testing.T) {
	for _, stage := range []string{"successful", "failed"} {
		t.Run(stage, func(t *testing.T) {
			native := &healthLifecycleNative{}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			first := newTestCycle(t, "inventory-session")
			if _, failure := source.GPUIdentities(context.Background(), first); failure != nil {
				t.Fatal(failure)
			}
			native.enumerationErr = connectionLoss()
			if _, failure := source.newCollectionReader(first).gpuIndices(context.Background()); failure.GetSourceError() != connectionNotValidStatus {
				t.Fatalf("connection failure = %v", failure)
			}
			native.enumerationErr = nil
			if stage == "failed" {
				native.initErr = errNoHostengine
			}
			if _, failure := source.GPUIdentities(context.Background(), newTestCycle(t, "inventory-recovery")); (failure != nil) != (stage == "failed") {
				t.Fatalf("inventory recovery = %v", failure)
			}
			registerMetricAndHealth(t, source)
			if native.watchCalls.Load() != 0 || native.healthWatchCalls.Load() != 0 {
				t.Fatal("inventory recovery or registration eagerly created watches")
			}
			native.initErr = nil
			if report, failure := source.newCollectionReader(newTestCycle(t, "health-after-recovery")).readHealth(context.Background()); failure != nil || report == nil {
				t.Fatalf("health registered after inventory recovery = %v, %v", report, failure)
			}
		})
	}
}

func TestRecoveryInitializationFailureReleasesOldClientOnce(t *testing.T) {
	for _, initFailure := range []error{errors.New("replacement Init is not ready"), connectionLoss()} {
		t.Run(initFailure.Error(), func(t *testing.T) {
			gate := &sync.Mutex{}
			oldWatch := &fakeHealthWatch{}
			native := &healthLifecycleNative{cleanupNative: cleanupNative{nativeCallGate: gate}, health: oldWatch}
			source := newSource(native, gate)
			t.Cleanup(func() { _ = source.Close() })
			registerMetricAndHealth(t, source)
			first := newTestCycle(t, "old-session")
			values := source.ReadGPUMetrics(context.Background(), first, 0, []string{signalPowerDraw})
			if values[signalPowerDraw].Value == nil {
				t.Fatalf("initial metrics failed: %v", values)
			}
			if _, failure := source.newCollectionReader(first).readHealth(context.Background()); failure != nil {
				t.Fatal(failure)
			}
			native.enumerationErr = connectionLoss()
			if _, failure := source.newCollectionReader(first).gpuIndices(context.Background()); failure.GetSourceError() != connectionNotValidStatus {
				t.Fatalf("old connection failure lost: %v", failure)
			}
			native.enumerationErr, native.initErr = nil, initFailure
			retry := newTestCycle(t, "replacement-session")
			_, failure := source.newCollectionReader(retry).readHealth(context.Background())
			if failure.GetDetail() != initFailure.Error() {
				t.Fatalf("replacement initialization failure lost: %v", failure)
			}
			if err := source.AddMetricsToWatch([]string{signalTemperature}); err == nil {
				t.Fatal("failed recovery reopened metric registration after watch collection")
			}
			if err := source.AddHealthToWatch(source.healthSystems); err == nil {
				t.Fatal("failed recovery reopened health registration after watch collection")
			}
			native.initErr = nil
			values = source.ReadGPUMetrics(context.Background(), retry, 0, []string{signalPowerDraw})
			if values[signalPowerDraw].CollectionError.GetDetail() != initFailure.Error() {
				t.Fatalf("same-cycle metrics retried initialization: %v", values)
			}
			if _, failure := source.newCollectionReader(retry).gpuIndices(context.Background()); failure.GetDetail() != initFailure.Error() {
				t.Fatalf("same-cycle enumeration retried initialization: %v", failure)
			}
			if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.watchCalls.Load() != 1 || native.healthWatchCalls.Load() != 1 || native.readCalls.Load() != 1 || native.enumerationCalls.Load() != 1 || oldWatch.readCalls.Load() != 1 || oldWatch.cleanupCalls.Load() != 0 || native.watchCleanupCalls.Load() != 0 {
				t.Fatal("replacement initialization retried or consumed old session ownership more than once")
			}
			newWatch := &fakeHealthWatch{}
			native.health = newWatch
			next := newTestCycle(t, "replacement-session")
			values = source.ReadGPUMetrics(context.Background(), next, 0, []string{signalPowerDraw})
			if values[signalPowerDraw].Value == nil {
				t.Fatalf("later-cycle initialization did not retry: %v", values)
			}
			if _, failure := source.newCollectionReader(next).readHealth(context.Background()); failure != nil {
				t.Fatalf("later-cycle health did not recover: %v", failure)
			}
			if native.initCalls.Load() != 3 || native.cleanupCalls.Load() != 1 || native.watchCalls.Load() != 2 || native.healthWatchCalls.Load() != 2 {
				t.Fatal("later-cycle recovery did not replace both watches with one client")
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if native.cleanupCalls.Load() != 2 || native.watchCleanupCalls.Load() != 1 || newWatch.cleanupCalls.Load() != 1 || oldWatch.cleanupCalls.Load() != 0 {
				t.Fatal("final Close repeated old ownership or lost replacement resources")
			}
		})
	}
}

func TestConnectionLossRebuildsBothFeaturesOnNextCycle(t *testing.T) {
	gate := &sync.Mutex{}
	oldWatch := &fakeHealthWatch{readError: connectionLoss()}
	native := &recoveryNative{healthLifecycleNative: &healthLifecycleNative{
		cleanupNative: cleanupNative{nativeCallGate: gate}, health: oldWatch,
	}}
	source := newSource(native, gate)
	t.Cleanup(func() { _ = source.Close() })
	registerMetricAndHealth(t, source)
	first := newTestCycle(t, "session-recovery")
	if field := source.ReadGPUMetrics(context.Background(), first, 0, []string{signalPowerDraw})[signalPowerDraw]; field.Value == nil {
		t.Fatalf("initial metric read failed: %v", field)
	}
	if _, failure := source.newCollectionReader(first).readHealth(context.Background()); failure.GetSourceError() != connectionNotValidStatus {
		t.Fatalf("connection error was lost: %v", failure)
	}
	if native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 {
		t.Fatal("failing read retried initialization or released the session")
	}
	for range 2 {
		if _, failure := source.newCollectionReader(first).readHealth(context.Background()); failure.GetSourceError() != connectionNotValidStatus {
			t.Fatalf("same-cycle health read lost connection failure: %v", failure)
		}
		field := source.ReadGPUMetrics(context.Background(), first, 0, []string{signalPowerDraw})[signalPowerDraw]
		if field.CollectionError.GetSourceError() != connectionNotValidStatus {
			t.Fatalf("same-cycle metric read lost connection failure: %v", field)
		}
	}
	if oldWatch.readCalls.Load() != 1 || native.readCalls.Load() != 1 || native.initCalls.Load() != 1 {
		t.Fatal("another operation entered the failed session")
	}
	monitoredSince := timestamppb.New(time.Unix(200, 0))
	newWatch := &fakeHealthWatch{report: &observation.DCGMHealthReport{MonitoredSince: monitoredSince}}
	native.health = newWatch
	// Cycle identity, rather than a reused correlation string, bounds a session.
	next := newTestCycle(t, "session-recovery")
	if field := source.ReadGPUMetrics(context.Background(), next, 0, []string{signalPowerDraw})[signalPowerDraw]; field.Value == nil {
		t.Fatalf("metrics did not recover: %v", field)
	}
	report, failure := source.newCollectionReader(next).readHealth(context.Background())
	if failure != nil || report.GetMonitoredSince().AsTime() != monitoredSince.AsTime() {
		t.Fatalf("health did not recover with native monitoring epoch: %v, %v", report, failure)
	}
	if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.watchCalls.Load() != 2 || native.healthWatchCalls.Load() != 2 {
		t.Fatal("recovery did not replace both watches and balance the old client")
	}
	if native.watchCleanupCalls.Load() != 0 || oldWatch.cleanupCalls.Load() != 0 {
		t.Fatal("stale cleanup handles were used after connection loss")
	}
	if err := source.AddMetricsToWatch([]string{signalTemperature}); err == nil {
		t.Fatal("reconnection reopened signal registration")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if native.cleanupCalls.Load() != 2 || native.watchCleanupCalls.Load() != 1 || newWatch.cleanupCalls.Load() != 1 || oldWatch.cleanupCalls.Load() != 0 {
		t.Fatal("final Close repeated old ownership or lost the replacement watch")
	}
}

func TestConnectionLossIsRecognizedInIdentityAndInventory(t *testing.T) {
	for _, operation := range []string{"enumeration", "UUID call", "UUID field", "inventory call", "inventory entry", "inventory field"} {
		t.Run(operation, func(t *testing.T) {
			native := &fakeNative{}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			cycle := newTestCycle(t, "lost-"+operation)
			failure := &observation.CollectionError{
				SourceError: connectionNotValidStatus,
				Category:    observation.CollectionErrorCategoryUnavailable, Detail: "connection lost in field",
			}
			switch operation {
			case "enumeration":
				native.enumerationErr = connectionLoss()
				_, _ = source.newCollectionReader(cycle).gpuIndices(context.Background())
			case "UUID call", "UUID field":
				if operation == "UUID call" {
					native.uuidErr = connectionLoss()
				} else {
					native.uuidEntries = map[uint]GPUIdentityEntry{0: {Index: 0, UUIDError: failure}}
				}
				_ = source.newCollectionReader(cycle).gpuUUIDs(context.Background(), []uint{0})
			default:
				entry := &observation.GPUInventoryEntry{}
				switch operation {
				case "inventory call":
					native.inventoryErr = connectionLoss()
				case "inventory entry":
					entry.CollectionError = failure
				case "inventory field":
					entry.FieldErrors = map[string]*observation.CollectionError{"model": failure}
				}
				native.inventoryEntries = map[uint]*observation.GPUInventoryEntry{0: entry}
				_ = source.GPUInventory(context.Background(), cycle, []GPUIdentityEntry{{Index: 0}})
			}
			native.enumerationErr, native.uuidErr, native.inventoryErr = nil, nil, nil
			if _, failure := source.newCollectionReader(cycle).gpuIndices(context.Background()); failure.GetSourceError() != connectionNotValidStatus {
				t.Fatalf("field/call error did not invalidate the session: %v", failure)
			}
			if native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 {
				t.Fatal("failed collection reset its own session")
			}
			if indices, failure := source.newCollectionReader(newTestCycle(t, "recovered")).gpuIndices(context.Background()); failure != nil || len(indices) != 1 {
				t.Fatalf("next-cycle discovery did not recover: %v, %v", indices, failure)
			}
			if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 {
				t.Fatal("session replacement was not balanced")
			}
		})
	}
}

func TestJoinedConnectionLossResetsSessionOnNextCycle(t *testing.T) {
	queryFailure := &dcgmError{
		sourceError: "DCGM_ST_GENERIC_ERROR",
		category:    observation.CollectionErrorCategorySourceError, detail: "enumeration failed",
	}
	lost := connectionLoss()
	native := &fakeNative{enumerationErr: errors.Join(queryFailure, lost)}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	cycle := newTestCycle(t, "joined-connection-loss")
	if _, failure := source.newCollectionReader(cycle).gpuIndices(context.Background()); failure.GetSourceError() != queryFailure.sourceError {
		t.Fatalf("leading native error was not preserved: %v", failure)
	}
	session := source.session
	if session == nil || !errors.Is(session.connectionFailure, errConnectionNotValid) ||
		!errors.Is(session.connectionFailure, queryFailure) || !errors.Is(session.connectionFailure, lost) {
		t.Fatalf("session did not retain connection loss behind another native error: %v", session)
	}
	native.enumerationErr = nil
	if _, failure := source.newCollectionReader(cycle).gpuIndices(context.Background()); failure == nil {
		t.Fatal("same-cycle enumeration entered the invalid session")
	}
	if native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 || native.enumerationCalls.Load() != 1 {
		t.Fatal("failing cycle retried native work or replaced the session")
	}
	if indices, failure := source.newCollectionReader(newTestCycle(t, "recovered")).gpuIndices(context.Background()); failure != nil || len(indices) != 1 {
		t.Fatalf("connection loss behind another native error prevented recovery: %v, %v", indices, failure)
	}
	if source.session == nil || source.session.connectionFailure != nil || native.initCalls.Load() != 2 ||
		native.cleanupCalls.Load() != 1 || native.enumerationCalls.Load() != 2 {
		t.Fatal("next-cycle enumeration did not replace the invalid session once")
	}
}

func TestMetricWatchConnectionLossRetriesOnNextCycle(t *testing.T) {
	setupFailure := &dcgmError{
		sourceError: "DCGM_ST_GENERIC_ERROR",
		category:    observation.CollectionErrorCategorySourceError, detail: "watch setup failed",
	}
	rollbackFailure := errors.New("watch rollback failed")
	for _, test := range []struct {
		setupError    error
		rollbackError error
		name          string
	}{
		{name: "setup connection loss", setupError: connectionLoss()},
		{name: "setup connection loss and rollback failure", setupError: connectionLoss(), rollbackError: rollbackFailure},
		{name: "setup failure and rollback connection loss", setupError: setupFailure, rollbackError: connectionLoss()},
	} {
		t.Run(test.name, func(t *testing.T) {
			gate := &sync.Mutex{}
			native := &cleanupNative{setupError: test.setupError, cleanupError: test.rollbackError, nativeCallGate: gate}
			source := newSource(native, gate)
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddMetricsToWatch([]string{signalPowerDraw}); err != nil {
				t.Fatal(err)
			}
			cycle := newTestCycle(t, "failed-metric-watch")
			values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
			session := source.session
			if values[signalPowerDraw].CollectionError == nil || session == nil || !errors.Is(session.connectionFailure, errConnectionNotValid) {
				t.Fatalf("metric watch failure did not invalidate the session: %v", values)
			}
			if !errors.Is(session.connectionFailure, test.setupError) || (test.rollbackError != nil && !errors.Is(session.connectionFailure, test.rollbackError)) {
				t.Fatalf("watch setup or rollback cause was lost: %v", session.connectionFailure)
			}
			if source.closed.Load() || native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 || native.watchCalls.Load() != 1 || native.watchCleanupCalls.Load() != 1 || native.readCalls.Load() != 0 {
				t.Fatal("metric-only watch connection loss closed the Source, retried setup, or released the client")
			}
			native.setupError, native.cleanupError = nil, nil
			values = source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw})
			if values[signalPowerDraw].CollectionError == nil || native.watchCalls.Load() != 1 || native.initCalls.Load() != 1 || native.cleanupCalls.Load() != 0 || native.readCalls.Load() != 0 {
				t.Fatal("same-cycle metric read entered the invalid session")
			}
			values = source.ReadGPUMetrics(context.Background(), newTestCycle(t, "new-metric-session"), 0, []string{signalPowerDraw})
			if values[signalPowerDraw].Value == nil || values[signalPowerDraw].CollectionError != nil {
				t.Fatalf("metric-only watch setup did not recover: %v", values)
			}
			if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 || native.watchCalls.Load() != 2 || native.watchCleanupCalls.Load() != 1 || native.readCalls.Load() != 1 {
				t.Fatal("next cycle did not rebuild metric client/watch exactly once")
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if native.cleanupCalls.Load() != 2 || native.watchCleanupCalls.Load() != 2 || native.cleanupOutsideGate.Load() {
				t.Fatal("cleanup repeated failed rollback or lost the recovered watch/client")
			}
		})
	}
}

func TestConnectionLossInMetricFieldsPreservesOtherValues(t *testing.T) {
	native := &recoveryNative{healthLifecycleNative: &healthLifecycleNative{}, metricValues: map[string]FieldValue{
		signalPowerDraw: {Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 7}}},
		signalTemperature: {CollectionError: &observation.CollectionError{
			SourceError: connectionNotValidStatus, Category: observation.CollectionErrorCategoryUnavailable,
		}},
	}}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddMetricsToWatch([]string{signalPowerDraw, signalTemperature}); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "partial-metrics")
	values := source.ReadGPUMetrics(context.Background(), cycle, 0, []string{signalPowerDraw, signalTemperature})
	if values[signalPowerDraw].Value.GetIntValue() != 7 || values[signalTemperature].CollectionError.GetSourceError() != connectionNotValidStatus {
		t.Fatalf("partial metric fields were discarded: %v", values)
	}
	if _, failure := source.newCollectionReader(cycle).gpuIndices(context.Background()); failure.GetSourceError() != connectionNotValidStatus {
		t.Fatalf("field status did not invalidate the shared session: %v", failure)
	}
	if source.closed.Load() {
		t.Fatal("connection loss permanently closed the metric-only Source")
	}
	native.metricValues = nil
	if values := source.ReadGPUMetrics(context.Background(), newTestCycle(t, "new-session"), 0, []string{signalPowerDraw}); values[signalPowerDraw].Value == nil {
		t.Fatalf("metric-only Source did not recover: %v", values)
	}
}

func TestSessionReleaseIsConsumedEvenWhenItFails(t *testing.T) {
	for _, releaseError := range []error{connectionLoss(), errors.New("shutdown failed")} {
		t.Run(releaseError.Error(), func(t *testing.T) {
			native := &recoveryNative{releaseError: releaseError, healthLifecycleNative: &healthLifecycleNative{}}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			native.enumerationErr = connectionLoss()
			_, _ = source.newCollectionReader(newTestCycle(t, "lost")).gpuIndices(context.Background())
			native.enumerationErr, native.releaseError = nil, nil
			_, failure := source.newCollectionReader(newTestCycle(t, "release")).gpuIndices(context.Background())
			if errors.Is(releaseError, errConnectionNotValid) {
				if failure != nil {
					t.Fatalf("expected invalid-session release prevented recovery: %v", failure)
				}
			} else {
				if failure == nil {
					t.Fatal("shutdown failure was hidden")
				}
				if _, nextFailure := source.newCollectionReader(newTestCycle(t, "retry-init")).gpuIndices(context.Background()); nextFailure != nil {
					t.Fatalf("later initialization did not retry: %v", nextFailure)
				}
			}
			if native.cleanupCalls.Load() != 1 || native.initCalls.Load() != 2 {
				t.Fatal("release was retried or replacement initialization was lost")
			}
		})
	}
}

type delayedConnectionLossNative struct {
	*fakeNative
	entered chan struct{}
	release chan struct{}
}

func (native *delayedConnectionLossNative) AllGPUIndices() ([]uint, error) {
	if native.enumerationCalls.Add(1) == 1 {
		close(native.entered)
		<-native.release
		return nil, connectionLoss()
	}
	return []uint{0}, nil
}

func TestLateConnectionLossWaitsForNativeCallBeforeRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		native := &delayedConnectionLossNative{fakeNative: &fakeNative{}, entered: make(chan struct{}), release: make(chan struct{})}
		release := sync.OnceFunc(func() { close(native.release) })
		defer release()
		source := newSource(native, &sync.Mutex{})
		defer func() { _ = source.Close() }()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			_, _ = source.newCollectionReader(newTestCycle(t, "blocked")).gpuIndices(ctx)
		}()
		<-native.entered
		cancel()
		<-finished
		if _, failure := source.newCollectionReader(newTestCycle(t, "still-busy")).gpuIndices(context.Background()); failure.GetCategory() != observation.CollectionErrorCategoryUnavailable || native.cleanupCalls.Load() != 0 {
			t.Fatalf("recovery raced an unfinished native call: %v", failure)
		}
		release()
		synctest.Wait()
		if indices, failure := source.newCollectionReader(newTestCycle(t, "recovered")).gpuIndices(context.Background()); failure != nil || len(indices) != 1 {
			t.Fatalf("late native error was not recovered: %v, %v", indices, failure)
		}
		if native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 {
			t.Fatal("late-error session was not replaced exactly once")
		}
	})
}
