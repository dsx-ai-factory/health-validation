// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectbridge

import (
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCanonicalTargetKinds(t *testing.T) {
	value := func(s collect.Signal, target *observation.ObservationTarget) *observation.Observation {
		return &observation.Observation{
			SignalId: s.ID(), Source: "test", Target: target, ObservedAt: timestamppb.New(time.Unix(1, 0)), Unit: s.Unit(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 1}}},
		}
	}
	draw := gpusignals.PowerDraw
	if err := validateCollectObservation(value(draw, observation.GPUTarget("GPU-1")), draw); err != nil {
		t.Fatalf("GPU target rejected: %v", err)
	}
	err := validateCollectObservation(value(draw, observation.NodeTarget("")), draw)
	if err == nil || !strings.Contains(err.Error(), "requires a GPU target") {
		t.Fatalf("node target for a GPU signal: err=%v", err)
	}

	at := timestamppb.New(time.Unix(1, 0))
	lost := &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}
	for _, test := range []struct {
		o       *observation.Observation
		signal  collect.Signal
		name    string
		wantErr string
	}{
		{name: "P2P on a pair", signal: gpusignals.NVLinkP2PStatus, o: value(gpusignals.NVLinkP2PStatus, observation.GPUPairTarget("GPU-1", "GPU-2"))},
		{name: "P2P value on a GPU", signal: gpusignals.NVLinkP2PStatus, o: value(gpusignals.NVLinkP2PStatus, observation.GPUTarget("GPU-1")), wantErr: "requires a GPU pair target"},
		{
			name: "P2P failure on its source GPU", signal: gpusignals.NVLinkP2PStatus,
			o: observation.NewCollectionErrorObservation(gpusignals.NVLinkP2PStatus.ID(), gpusignals.NVLinkP2PStatus.Unit(), "test", "read", observation.GPUTarget("GPU-1"), at, lost),
		},
		{name: "link speed on an endpoint", signal: gpusignals.NVLinkEndpointSpeed, o: value(gpusignals.NVLinkEndpointSpeed, observation.NVLinkEndpointTarget("GPU-1", 3))},
		{name: "link speed value on a GPU", signal: gpusignals.NVLinkEndpointSpeed, o: value(gpusignals.NVLinkEndpointSpeed, observation.GPUTarget("GPU-1")), wantErr: "NVLink endpoint target"},
		{name: "link count on a GPU", signal: gpusignals.NVLinkLinkCount, o: value(gpusignals.NVLinkLinkCount, observation.GPUTarget("GPU-1"))},
		{name: "P2P on a self-pair", signal: gpusignals.NVLinkP2PStatus, o: value(gpusignals.NVLinkP2PStatus, observation.GPUPairTarget("GPU-1", "GPU-1")), wantErr: "two distinct GPUs"},
	} {
		err := validateCollectObservation(test.o, test.signal)
		if (err == nil) != (test.wantErr == "") || (err != nil && !strings.Contains(err.Error(), test.wantErr)) {
			t.Errorf("%s: err=%v, want %q", test.name, err, test.wantErr)
		}
	}

	// A later entry overrides an earlier one, so a per-link signal can be
	// carved out of the whole-GPU entry.
	index := canonicalIndex([]canonicalTarget{
		{kind: targetGPU, signals: gpusignals.All()},
		{kind: targetNVLinkEndpoint, signals: collect.Signals{draw}},
	})
	if got := index[draw.ID()].target; got != targetNVLinkEndpoint {
		t.Fatalf("override target=%q, want %q", got, targetNVLinkEndpoint)
	}
	if got := index[gpusignals.PowerLimitEnforced.ID()].target; got != targetGPU {
		t.Fatalf("untouched target=%q, want %q", got, targetGPU)
	}
}

func TestJournalEntryTargetRules(t *testing.T) {
	at := timestamppb.New(time.Unix(1, 0))
	value := func(s collect.Signal, target *observation.ObservationTarget) *observation.Observation {
		return &observation.Observation{
			SignalId: s.ID(), Source: "test", Target: target, ObservedAt: at, Unit: s.Unit(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 1}}},
		}
	}
	code, marker := xidsignals.Code, xidsignals.ScanWindow
	tests := []struct {
		o       *observation.Observation
		signal  collect.Signal
		name    string
		wantErr string
	}{
		{name: "event on journal entry", o: value(code, observation.JournalEntryTarget("b", "c")), signal: code},
		{name: "event on node", o: value(code, observation.NodeTarget("")), signal: code, wantErr: `signal "xid.event.code" requires a journal entry target`},
		{name: "marker on node", o: value(marker, observation.NodeTarget("")), signal: marker},
		{name: "marker on journal entry", o: value(marker, observation.JournalEntryTarget("b", "c")), signal: marker, wantErr: "requires a node target"},
		{name: "blank boot ID", o: value(code, observation.JournalEntryTarget("", "c")), signal: code, wantErr: "journal entry target requires a boot ID and cursor"},
		{name: "blank cursor", o: value(code, observation.JournalEntryTarget("b", " ")), signal: code, wantErr: "journal entry target requires a boot ID and cursor"},
		{
			name:   "untargeted failure",
			o:      observation.NewCollectionErrorObservation(code.ID(), code.Unit(), "test", "read", nil, at, &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}),
			signal: code,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCollectObservation(test.o, test.signal)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("err=%v, want %q", err, test.wantErr)
			}
		})
	}
}
