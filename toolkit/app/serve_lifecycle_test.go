// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
	"github.com/dsx-ai-factory/health-validation/toolkit/internal/gpuinventory"
)

type cycleReleaseHost struct {
	hostBackend
	release func() error
}

func (h cycleReleaseHost) releaseCycle() error { return h.release() }

func TestAgentReleasesResourcesAfterEachCycle(t *testing.T) {
	for _, releaseErr := range []error{nil, errors.New("shutdown failed")} {
		var releases atomic.Int32
		a := cadenceRunApp()
		a.host = cycleReleaseHost{hostBackend: a.host, release: func() error { releases.Add(1); return releaseErr }}
		agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
		for cycle := int32(1); cycle <= 2; cycle++ {
			verdict, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
			if err != nil || verdict == nil {
				t.Fatalf("cycle %d: %v, %v", cycle, verdict, err)
			}
			waitForAgentIdle(t, agent)
			if releases.Load() != cycle {
				t.Fatalf("release calls=%d, want %d", releases.Load(), cycle)
			}
		}
	}
}

func TestAgentWaitsForNativeWorkBeforeReleasingResources(t *testing.T) {
	backend := &blockingAgentSource{started: make(chan struct{}, 1), release: make(chan struct{})}
	defer close(backend.release)
	released := make(chan struct{})
	a := withAgentSource(cadenceRunApp(), backend)
	a.host = cycleReleaseHost{hostBackend: a.host, release: func() error { close(released); return nil }}
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := agent.evaluate(ctx, &healthv1.EvaluateRequest{}); done <- err }()
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("native call did not start")
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not return")
	}
	select {
	case <-released:
		t.Fatal("released resources during native work")
	default:
	}
	backend.release <- struct{}{}
	waitForAgentIdle(t, agent)
	select {
	case <-released:
	default:
		t.Fatal("native completion did not release resources")
	}
}

func TestAgentReportsBusyWhileResourceReleaseIsStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		var releases int
		a := cadenceRunApp()
		a.host = cycleReleaseHost{hostBackend: a.host, release: func() error {
			releases++
			if releases == 1 {
				close(entered)
				<-release
			}
			return nil
		}}
		agent := a.newVerdictAgent(&Config{CollectionTimeoutSeconds: 1}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
		request := &healthv1.EvaluateRequest{}
		if _, err := agent.evaluate(context.Background(), request); err != nil {
			t.Fatal(err)
		}
		<-entered
		started := time.Now()
		type result struct {
			verdict *healthv1.NodeVerdict
			err     error
		}
		queued := make(chan result, 1)
		go func() {
			verdict, err := agent.evaluate(context.Background(), request)
			queued <- result{verdict: verdict, err: err}
		}()
		synctest.Wait()
		select {
		case got := <-queued:
			t.Fatalf("request returned before release timed out: %v, %v", got.verdict, got.err)
		default:
		}

		assertBusy := func(verdict *healthv1.NodeVerdict, err error) {
			t.Helper()
			checks := verdict.GetCheckResults()
			if err != nil || verdict.GetOverallSeverity() != healthv1.Severity_UNKNOWN || len(checks) != 1 ||
				checks[0].GetCheckId() != "collection_source_busy" ||
				checks[0].GetObservedValues()["source"].GetStringValue() != "nvml.shutdown" {
				t.Fatalf("expected busy verdict for NVML shutdown, got %v, %v", verdict, err)
			}
		}
		got := <-queued
		assertBusy(got.verdict, got.err)
		assertBusy(agent.evaluate(context.Background(), request))
		if elapsed := time.Since(started); elapsed != time.Second || releases != 1 {
			t.Fatalf("cleanup blocked requests for %s or overlapped with another release (calls=%d)", elapsed, releases)
		}

		unblock()
		waitForAgentIdle(t, agent)
		verdict, err := agent.evaluate(context.Background(), request)
		if err != nil || verdict.GetOverallSeverity() != healthv1.Severity_PASS {
			t.Fatalf("collection did not resume after release: %v, %v", verdict, err)
		}
		waitForAgentIdle(t, agent)
	})
}

func TestAgentReportsBusyWhileInventoryReadIsStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var entered atomic.Int32
		reader := gatedReader{
			gate:    new(sync.Mutex),
			entered: &entered,
			release: make(chan struct{}),
			done:    make(chan struct{}),
		}
		release := sync.OnceFunc(func() { close(reader.release) })
		defer release()

		a := cadenceRunApp()
		host := fakeHost{b: spyBackend{called: new(bool)}}
		a.host = sharedInventoryHost{
			fakeHost:  host,
			inventory: gpuinventory.New(func() (gpuinventory.Reader, error) { return reader, nil }),
			gpu:       host.gpuSensors(),
		}
		agent := a.newVerdictAgent(&Config{CollectionTimeoutSeconds: 1}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
		ctx := context.Background()
		request := &healthv1.EvaluateRequest{}
		evaluateFailure := func(id string, severity healthv1.Severity, steps ...string) *healthv1.CheckVerdict {
			t.Helper()
			verdict, err := agent.evaluate(ctx, request)
			checks := verdict.GetCheckResults()
			if err != nil || verdict.GetOverallSeverity() != severity || len(checks) != 1 ||
				checks[0].GetCheckId() != id || checks[0].GetSeverity() != severity {
				t.Fatalf("verdict = %v, err = %v, want one %s result with severity %v", verdict, err, id, severity)
			}
			check := checks[0]
			for _, action := range []*healthv1.RecommendedAction{verdict.GetRecommendedAction(), check.GetRecommendedAction()} {
				if action.GetOperationalState() != healthv1.OperationalState_drain || !slices.Equal(action.GetRemediationActions(), steps) {
					t.Fatalf("%s action = %v, want drain with %v", id, action, steps)
				}
			}
			return check
		}

		evaluateFailure("architecture_discovery", healthv1.Severity_CRITICAL, "reset_gpu", "restart_os", "service_required")
		if n := entered.Load(); n != 1 {
			t.Fatalf("inventory reads after timeout = %d, want 1", n)
		}

		busy := evaluateFailure("collection_source_busy", healthv1.Severity_UNKNOWN, "retry_collection")
		if source := busy.GetObservedValues()["source"].GetStringValue(); source != "nvml" {
			t.Fatalf("busy source = %q, want nvml", source)
		}
		if n := entered.Load(); n != 1 {
			t.Fatalf("inventory reads while busy = %d, want 1", n)
		}

		release()
		waitForAgentIdle(t, agent)
		recovered, err := agent.evaluate(ctx, request)
		if err != nil || recovered.GetOverallSeverity() != healthv1.Severity_PASS {
			t.Fatalf("collection after release = %v, %v, want PASS", recovered, err)
		}
		for _, check := range recovered.GetCheckResults() {
			if check.GetCheckId() == "collection_source_busy" {
				t.Fatal("busy verdict remained after inventory read returned")
			}
		}
		if n := entered.Load(); n != 2 {
			t.Fatalf("inventory reads after recovery = %d, want 2", n)
		}
		waitForAgentIdle(t, agent)
	})
}

func TestAgentDiscoveryFailureStillReleasesResources(t *testing.T) {
	var releases atomic.Int32
	a := cadenceRunApp()
	a.host = cycleReleaseHost{hostBackend: erroringHost{}, release: func() error { releases.Add(1); return nil }}
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	verdict, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
	if err != nil || verdict.GetOverallSeverity() != healthv1.Severity_CRITICAL {
		t.Fatalf("discovery failure: %v, %v", verdict, err)
	}
	waitForAgentIdle(t, agent)
	if releases.Load() != 1 {
		t.Fatal("discovery failure skipped resource release")
	}
}
