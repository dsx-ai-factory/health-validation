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
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// xidAppSensor stands in for the journal Xid sensor under a WithCollect claim.
type xidAppSensor struct {
	calls *int
	obs   []*observation.Observation
}

func (xidAppSensor) ID() string               { return "test.xid" }
func (xidAppSensor) Signals() collect.Signals { return xidsignals.All() }
func (s xidAppSensor) Measure(_ context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	*s.calls++
	return &observation.ObservationBatch{CollectionId: collect.NewOp(opts...).Cycle().CollectionID(), Observations: s.obs}, nil
}

func xidAppValue(s collect.Signal, target *observation.ObservationTarget, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "test.xid", Target: target, ObservedAt: timestamppb.New(time.Unix(1, 0)),
		Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

func xidAppMarker() *observation.Observation {
	return xidAppValue(xidsignals.ScanWindow, observation.NodeTarget(""), &observation.Value{Kind: &observation.Value_StringValue{StringValue: xidsignals.WindowBoot}})
}

func runXidApp(t *testing.T, a *App, wantCode int) map[string]health.CheckVerdict {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code != wantCode {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, wantCode, stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	out := map[string]health.CheckVerdict{}
	for _, result := range verdict.CheckResults {
		if strings.HasPrefix(result.CheckID, "gpu_xid_") {
			out[result.CheckID] = result
		}
	}
	if skipped := architectureSkips(verdict); len(out)+skipped != len(xid.Checks()) {
		t.Fatalf("got %d Xid verdicts and %d architecture skips, want %d", len(out), skipped, len(xid.Checks()))
	}
	return out
}

// architectureSkips counts the Xid checks skipped as inapplicable to the
// catalog's architecture. Other skips would hide a domain that never ran.
func architectureSkips(verdict health.NodeVerdict) int {
	n := 0
	for _, s := range verdict.SkippedChecks {
		if s.Category == health.Xid && s.SkipReason == health.ArchitectureUnsupported {
			n++
		}
	}
	return n
}

func TestRunCommandCollectsXidThroughBridge(t *testing.T) {
	calls := 0
	entry := observation.JournalEntryTarget("b", "c1")
	a := cadenceRunApp()
	a.xid = nil
	a.WithCollect(xidAppSensor{calls: &calls, obs: []*observation.Observation{
		xidAppValue(xidsignals.Code, entry, &observation.Value{Kind: &observation.Value_IntValue{IntValue: 79}}),
		xidAppValue(xidsignals.PCIAddress, entry, &observation.Value{Kind: &observation.Value_StringValue{StringValue: "0000:01:00"}}),
		xidAppMarker(),
	}}, xidsignals.All().IDs()...)
	verdicts := runXidApp(t, a, exitUnhealthy)

	var want health.RecommendedAction
	for _, check := range xid.Checks() {
		if check.ID == "gpu_xid_79" {
			want = check.RecommendedAction
		}
	}
	if got := verdicts["gpu_xid_79"]; got.Severity != health.Critical || !got.RecommendedAction.Equal(want) {
		t.Fatalf("gpu_xid_79 = %v %v, want CRITICAL %v", got.Severity, got.RecommendedAction, want)
	}
	if got := verdicts["gpu_xid_48"]; got.Severity != health.Pass {
		t.Fatalf("gpu_xid_48 = %v, want PASS", got.Severity)
	}
	if calls != 1 {
		t.Fatalf("claimed Xid sensor measured %d times, want 1", calls)
	}
}

func TestRunCommandXidCleanScanPasses(t *testing.T) {
	calls := 0
	a := cadenceRunApp()
	a.xid = nil
	a.WithCollect(xidAppSensor{calls: &calls, obs: []*observation.Observation{xidAppMarker()}}, xidsignals.All().IDs()...)
	for id, v := range runXidApp(t, a, exitOK) {
		if v.Severity == health.Critical {
			t.Fatalf("%s = CRITICAL on a clean scan", id)
		}
	}
}

// TestWithCollectRejectsPartialXidClaim keeps one Xid event's fields from
// coming from producers that read the journal at different times.
func TestWithCollectRejectsPartialXidClaim(t *testing.T) {
	for name, claims := range map[string][]string{
		"fatal only":            {xidsignals.NVLink5Fatal.ID()},
		"code and address":      {xidsignals.Code.ID(), xidsignals.PCIAddress.ID()},
		"events without marker": xidsignals.EventSignals().IDs(),
		// The repeat makes the claim count match without covering the marker.
		"duplicate in place of marker": append(xidsignals.EventSignals().IDs(), xidsignals.Code.ID()),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			a := cadenceRunApp()
			a.xid = nil
			a.WithCollect(xidAppSensor{calls: &calls}, claims...)
			var stdout, stderr bytes.Buffer
			if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code != exitError || stdout.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q", code, stdout.String())
			}
			if !strings.Contains(stderr.String(), "claims only part of the Xid signals") || calls != 0 {
				t.Fatalf("stderr=%q calls=%d", stderr.String(), calls)
			}
		})
	}
}

func TestWithXidBackendFreesXidAdapterName(t *testing.T) {
	adapter := customCountAdapter()
	adapter.name = "xid"
	a := cadenceRunApp().WithCollect(&collectAppSensor{}).WithCollectAdapters(adapter)
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run", "--node-id", testNodeID}, &stdout, &stderr); code == exitError {
		t.Fatalf("caller xid adapter rejected while WithXidBackend is set: %q", stderr.String())
	}
}
