// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"github.com/dsx-ai-factory/health-validation/toolkit"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// linkFlapAppSensor stands in for the journal link-flap sensor under a
// WithCollect claim.
type linkFlapAppSensor struct {
	calls *int
	obs   []*observation.Observation
}

func (linkFlapAppSensor) ID() string               { return "test.link_flap" }
func (linkFlapAppSensor) Signals() collect.Signals { return ibsignals.LinkFlapSignals() }
func (s linkFlapAppSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	*s.calls++
	return &observation.ObservationBatch{CollectionId: collect.NewOp(opts...).Cycle().CollectionID(), Observations: s.obs}, nil
}

var linkFlapAppStart = time.Unix(1000, 0)

func linkFlapAppValue(s collect.Signal, target *observation.ObservationTarget, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "test.link_flap", Target: target, ObservedAt: timestamppb.New(linkFlapAppStart.Add(time.Hour)),
		Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

func linkFlapAppMarker() *observation.Observation {
	return linkFlapAppValue(ibsignals.LinkFlapScanSince, observation.NodeTarget(""),
		&observation.Value{Kind: &observation.Value_IntValue{IntValue: linkFlapAppStart.UnixMicro()}})
}

func linkFlapAppTransition(cursor, state string, after time.Duration) []*observation.Observation {
	target := observation.JournalEntryTarget("b", cursor)
	return []*observation.Observation{
		linkFlapAppValue(ibsignals.LinkTransitionInterface, target, &observation.Value{Kind: &observation.Value_StringValue{StringValue: "enp90s0np0"}}),
		linkFlapAppValue(ibsignals.LinkTransitionState, target, &observation.Value{Kind: &observation.Value_StringValue{StringValue: state}}),
		linkFlapAppValue(ibsignals.LinkTransitionRealtime, target, &observation.Value{Kind: &observation.Value_IntValue{IntValue: linkFlapAppStart.Add(after).UnixMicro()}}),
	}
}

// linkFlapApp is cadenceRunApp with the link-flap domain on the collect
// path, its signals claimed by a stand-in sensor.
func linkFlapApp(calls *int, obs ...*observation.Observation) *App {
	a := cadenceRunApp()
	a.rdmaLinkFlap = nil
	return a.WithCollect(linkFlapAppSensor{calls: calls, obs: obs}, ibsignals.LinkFlapSignals().IDs()...)
}

func runLinkFlapApp(t *testing.T, a *App, wantCode int) health.CheckVerdict {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code != wantCode {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, wantCode, stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	for _, result := range verdict.CheckResults {
		if result.CheckID == "rdma_linkflap_fatal" {
			return result
		}
	}
	t.Fatalf("rdma_linkflap_fatal not evaluated; skipped = %+v", verdict.SkippedChecks)
	return health.CheckVerdict{}
}

func TestRunCommandCollectsLinkFlapThroughBridge(t *testing.T) {
	calls := 0
	obs := append([]*observation.Observation{linkFlapAppMarker()}, linkFlapAppTransition("c1", ibsignals.LinkStateDown, time.Minute)...)
	obs = append(obs, linkFlapAppTransition("c2", ibsignals.LinkStateUp, 2*time.Minute)...)
	got := runLinkFlapApp(t, linkFlapApp(&calls, obs...), exitUnhealthy)
	if got.Severity != health.Critical || !got.RecommendedAction.Equal(infiniband.RDMALinkFlapFatal().RecommendedAction) {
		t.Fatalf("rdma_linkflap_fatal = %v %v, want CRITICAL with the check's action", got.Severity, got.RecommendedAction)
	}
	if calls != 1 {
		t.Fatalf("claimed link-flap sensor measured %d times, want 1", calls)
	}
}

func TestRunCommandLinkFlapCleanScanPasses(t *testing.T) {
	calls := 0
	if got := runLinkFlapApp(t, linkFlapApp(&calls, linkFlapAppMarker()), exitOK); got.Severity != health.Pass {
		t.Fatalf("rdma_linkflap_fatal = %v on a clean scan, want PASS", got.Severity)
	}
}

// TestRunCommandLinkFlapSilentSourceFails fails, rather than skips, the
// check when the source emits neither a marker nor a collection error: a
// skip would leave it out of overall_severity.
func TestRunCommandLinkFlapSilentSourceFails(t *testing.T) {
	calls := 0
	got := runLinkFlapApp(t, linkFlapApp(&calls), exitUnhealthy)
	if got.Severity != health.Critical || !got.RecommendedAction.Equal(health.Drain(health.RemediationActionCheckCollection)) {
		t.Fatalf("rdma_linkflap_fatal = %v %v, want CRITICAL check collection", got.Severity, got.RecommendedAction)
	}
}

// TestWithCollectRejectsPartialLinkFlapClaim keeps one transition's fields
// from coming from producers that read the journal at different times.
func TestWithCollectRejectsPartialLinkFlapClaim(t *testing.T) {
	for name, claims := range map[string][]string{
		"state only":                    {ibsignals.LinkTransitionState.ID()},
		"transitions without marker":    ibsignals.LinkTransitionSignals().IDs(),
		"duplicate in place of a field": append(ibsignals.LinkTransitionSignals().IDs()[:2], ibsignals.LinkFlapScanSince.ID(), ibsignals.LinkFlapScanSince.ID()),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			a := cadenceRunApp()
			a.rdmaLinkFlap = nil
			a.WithCollect(linkFlapAppSensor{calls: &calls}, claims...)
			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code != exitError || stdout.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q", code, stdout.String())
			}
			if !strings.Contains(stderr.String(), "claims only part of the RDMA link-flap signals") || calls != 0 {
				t.Fatalf("stderr=%q calls=%d", stderr.String(), calls)
			}
		})
	}
}

// TestWithRDMALinkFlapBackendKeepsLegacySensor runs the override instead of
// the built-in collect sensor and frees the infiniband adapter name.
func TestWithRDMALinkFlapBackendKeepsLegacySensor(t *testing.T) {
	a := cadenceRunApp().WithRDMALinkFlapBackend(toolkit.MockRDMALinkFlap(
		"Thu 2025-12-11 11:49:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link down",
		"Thu 2025-12-11 11:51:00 UTC host kernel: mlx5_core 0000:5a:00.0 enp90s0np0: Link up",
	))
	if got := runLinkFlapApp(t, a, exitUnhealthy); got.Severity != health.Critical {
		t.Fatalf("rdma_linkflap_fatal = %v, want CRITICAL from the legacy backend", got.Severity)
	}

	adapter := customCountAdapter()
	adapter.name = "infiniband"
	a = cadenceRunApp().WithCollect(&collectAppSensor{}).WithCollectAdapters(adapter)
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code == exitError {
		t.Fatalf("caller infiniband adapter rejected while WithRDMALinkFlapBackend is set: %q", stderr.String())
	}
	adapter = customCountAdapter()
	adapter.name = "infiniband"
	a = linkFlapApp(new(int), linkFlapAppMarker()).WithCollect(&collectAppSensor{}).WithCollectAdapters(adapter)
	stdout.Reset()
	stderr.Reset()
	if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code != exitError || !strings.Contains(stderr.String(), "reserved for a built-in domain") {
		t.Fatalf("caller infiniband adapter beside the built-in: exit=%d stderr=%q", code, stderr.String())
	}
}
