// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
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

func TestAgentKeepsAdmissionClosedDuringResourceRelease(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	a := cadenceRunApp()
	a.host = cycleReleaseHost{hostBackend: a.host, release: func() error { close(entered); <-release; return nil }}
	agent := a.newVerdictAgent(&Config{}, evaluationRequest{NodeID: testNodeID, ExecutionContext: health.TenantGuest}, nil)
	if _, err := agent.evaluate(context.Background(), &healthv1.EvaluateRequest{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("resource release did not start")
	}
	select {
	case agent.gate <- struct{}{}:
		<-agent.gate
		t.Fatal("admission reopened during native shutdown")
	default:
	}
	// A caller's budget still bounds its wait while cleanup owns admission.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := agent.admit(ctx, testNodeID); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled admission: %v", err)
	}
	release <- struct{}{}
	waitForAgentIdle(t, agent)
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
