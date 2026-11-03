// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestEvaluateOnceCreatesFreshCyclesAndAppliesRequestOptions(t *testing.T) {
	source := &collectAppSensor{}
	a := cadenceRunApp().WithChecks(mixedChecks()...).WithCollect(source).WithCollectAdapters(customCountAdapter())
	continuous, maintenance := health.Continuous, health.MaintenanceWindow
	for i, cadence := range []*health.SchedulingCadence{&continuous, &maintenance} {
		verdict, err := a.evaluateOnce(context.Background(), &Config{}, evaluationRequest{
			NodeID: testNodeID, Cadence: cadence, ExecutionContext: health.TenantGuest,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if verdict.NodeID != testNodeID {
			t.Fatalf("NodeID = %q, want %q", verdict.NodeID, testNodeID)
		}
		found := false
		for _, result := range verdict.CheckResults {
			if result.CheckID == "mixed_maintenance" {
				found = true
			}
		}
		if found != (i == 1) {
			t.Fatalf("maintenance check evaluated = %v on request %d", found, i)
		}
	}
	// Only the maintenance window needs this source. Run it again to verify
	// that process-lifetime source reuse does not reuse the collection cache.
	if _, err := a.evaluateOnce(context.Background(), &Config{}, evaluationRequest{
		NodeID: testNodeID, Cadence: &maintenance, ExecutionContext: health.TenantGuest,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if len(source.cycles) != 2 || source.cycles[0] == source.cycles[1] || source.cycles[0].CollectionID() == source.cycles[1].CollectionID() {
		t.Fatalf("expected distinct collection cycles, got %v", source.cycles)
	}
}

func TestEvaluateOnceAlreadyCanceledDoesNotTouchHardware(t *testing.T) {
	a := New()
	a.host = panicHost{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.evaluateOnce(ctx, &Config{}, evaluationRequest{NodeID: testNodeID}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

type canceledEvaluationSource struct {
	started chan struct{}
	stopped chan struct{}
}

func (*canceledEvaluationSource) ID() string { return "test.canceled" }
func (*canceledEvaluationSource) Signals() collect.Signals {
	return collect.Signals{collect.NewSignal(customCountSignal, "1")}
}

func (s *canceledEvaluationSource) Measure(ctx context.Context, _ ...collect.OpOption) (*observation.ObservationBatch, error) {
	close(s.started)
	<-ctx.Done()
	close(s.stopped)
	return nil, ctx.Err()
}

func TestEvaluateOnceCancellationReachesCollectSource(t *testing.T) {
	source := &canceledEvaluationSource{started: make(chan struct{}), stopped: make(chan struct{})}
	a := cadenceRunApp().WithChecks(mixedChecks()...).WithCollect(source).WithCollectAdapters(customCountAdapter())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() {
		_, err := a.evaluateOnce(ctx, &Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
		completed <- err
	}()
	select {
	case <-source.started:
	case <-time.After(5 * time.Second):
		t.Fatal("collect source did not start")
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("evaluation did not return after cancellation")
	}
	select {
	case <-source.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("collect source did not receive cancellation")
	}
}
