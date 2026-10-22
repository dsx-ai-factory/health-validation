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
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
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

// Port signals require a port target. A device target is accepted only for a
// failure outcome, which is how the source reports a device whose ports could
// not be listed; a device-scoped value would invent a port.
func TestInfiniBandTargetKinds(t *testing.T) {
	at := timestamppb.New(time.Unix(1, 0))
	value := func(s collect.Signal, target *observation.ObservationTarget) *observation.Observation {
		return &observation.Observation{
			SignalId: s.ID(), Source: "sysfs", Target: target, ObservedAt: at, Unit: s.Unit(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: "ACTIVE"}}},
		}
	}
	failure := func(s collect.Signal, target *observation.ObservationTarget) *observation.Observation {
		return &observation.Observation{
			SignalId: s.ID(), Source: "sysfs", Target: target, ObservedAt: at, Unit: s.Unit(),
			Outcome: &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}},
		}
	}
	port, device := observation.InfiniBandPortTarget("mlx5_0", 1), observation.InfiniBandDeviceTarget("mlx5_0")
	state, firmware := ibsignals.StateName, ibsignals.FirmwareVersion
	if err := validateCollectObservation(value(state, port), state); err != nil {
		t.Fatalf("port target rejected for a port signal: %v", err)
	}
	if err := validateCollectObservation(failure(state, device), state); err != nil {
		t.Fatalf("device-scoped failure rejected for a port signal: %v", err)
	}
	if err := validateCollectObservation(value(state, device), state); err == nil || !strings.Contains(err.Error(), "requires a InfiniBand port target") {
		t.Fatalf("device-scoped value for a port signal: err=%v", err)
	}
	if err := validateCollectObservation(value(state, observation.GPUTarget("GPU-1")), state); err == nil {
		t.Fatal("GPU target accepted for a port signal")
	}
	if err := validateCollectObservation(value(firmware, device), firmware); err != nil {
		t.Fatalf("device target rejected for a device signal: %v", err)
	}
	if err := validateCollectObservation(value(firmware, port), firmware); err == nil {
		t.Fatal("port target accepted for a device signal")
	}
}
