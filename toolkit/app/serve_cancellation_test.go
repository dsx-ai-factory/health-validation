// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

func TestAgentClientCancellationWaitsForWorkOrSourceWatchdog(t *testing.T) {
	for _, test := range []struct {
		name          string
		deadline      bool
		finishesEarly bool
	}{
		{"cancel then completion", false, true},
		{"deadline then completion", true, true},
		{"cancel then source timeout", false, false},
		{"deadline then source timeout", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				backend := &blockingAgentSource{started: make(chan struct{}, 3), release: make(chan struct{})}
				var once sync.Once
				defer once.Do(func() { close(backend.release) })
				agent := withAgentSource(cadenceRunApp(), backend).newVerdictAgent(&Config{CollectionTimeoutSeconds: 10}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
				var firstContext context.Context
				var cancel context.CancelFunc
				if test.deadline {
					firstContext, cancel = context.WithTimeout(context.Background(), time.Second)
				} else {
					firstContext, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				firstDone := make(chan error, 1)
				go func() { _, err := agent.evaluate(firstContext, &healthv1.EvaluateRequest{}); firstDone <- err }()
				<-backend.started
				type result struct {
					err     error
					verdict *healthv1.NodeVerdict
				}
				evaluate := func() <-chan result {
					done := make(chan result, 1)
					go func() {
						verdict, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
						done <- result{err: err, verdict: verdict}
					}()
					return done
				}
				queued := evaluate()
				synctest.Wait()
				time.Sleep(time.Second)
				if !test.deadline {
					cancel()
				}
				wantCode := codes.Canceled
				if test.deadline {
					wantCode = codes.DeadlineExceeded
				}
				if err := <-firstDone; status.Code(err) != wantCode {
					t.Fatalf("first request: %v", err)
				}
				later := evaluate()
				synctest.Wait()
				for _, done := range []<-chan result{queued, later} {
					select {
					case got := <-done:
						t.Fatalf("client cancellation prematurely completed a waiting request: %v, %v", got.verdict, got.err)
					default:
					}
				}
				wantSeverity := healthv1.Severity_PASS
				if test.finishesEarly {
					time.Sleep(time.Second)
					once.Do(func() { close(backend.release) })
				} else {
					time.Sleep(9 * time.Second)
					wantSeverity = healthv1.Severity_UNKNOWN
				}
				for _, done := range []<-chan result{queued, later} {
					got := <-done
					if got.err != nil || got.verdict.GetOverallSeverity() != wantSeverity {
						t.Fatalf("waiting caller: %v, %v", got.verdict, got.err)
					}
					if !test.finishesEarly && got.verdict.CheckResults[0].GetCheckId() != "collection_source_busy" {
						t.Fatal("source watchdog did not produce a busy-source verdict")
					}
				}
				if !test.finishesEarly {
					if backend.calls.Load() != 1 {
						t.Fatal("unfinished native call was overlapped")
					}
					once.Do(func() { close(backend.release) })
				}
				waitForAgentIdle(t, agent)
				verdict, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{})
				if err != nil || verdict.GetOverallSeverity() != healthv1.Severity_PASS {
					t.Fatalf("collection did not recover: %v, %v", verdict, err)
				}
				waitForAgentIdle(t, agent)
			})
		})
	}
}

func TestAgentLivenessFollowsStuckCallDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := &blockingAgentSource{started: make(chan struct{}, 3), release: make(chan struct{})}
		var once sync.Once
		defer once.Do(func() { close(backend.release) })
		agent := withAgentSource(cadenceRunApp(), backend).newVerdictAgent(&Config{CollectionTimeoutSeconds: 10}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
		agent.stuckCallDeadline = time.Minute
		if got := agent.liveness(); !got.OK {
			t.Fatal("idle agent must be live")
		}
		go func() { _, _ = agent.evaluate(context.Background(), &healthv1.EvaluateRequest{}) }()
		<-backend.started
		// A queued request holds no extra state; liveness must not wait on the gate.
		queued := make(chan struct{})
		go func() { _, _ = agent.evaluate(context.Background(), &healthv1.EvaluateRequest{}); close(queued) }()
		synctest.Wait()
		if got := agent.liveness(); !got.OK {
			t.Fatal("running call before its watchdog must be live")
		}
		// Past the watchdog (10s) but before the deadline.
		time.Sleep(30 * time.Second)
		if got := agent.liveness(); !got.OK {
			t.Fatalf("stuck call before deadline must be live: %+v", got)
		}
		time.Sleep(31 * time.Second)
		got := agent.liveness()
		if got.OK || len(got.StuckCalls) != 1 || got.StuckCalls[0].Since.IsZero() {
			t.Fatalf("stuck call past deadline must fail liveness: %+v", got)
		}
		once.Do(func() { close(backend.release) })
		synctest.Wait()
		<-queued
		waitForAgentIdle(t, agent)
		if got := agent.liveness(); !got.OK {
			t.Fatalf("liveness must recover when the call returns: %+v", got)
		}
	})
}
