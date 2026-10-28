// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var flapStart = time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)

func flapValue(source string, s collect.Signal, target *observation.ObservationTarget, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: source, Target: target, ObservedAt: timestamppb.New(flapStart.Add(time.Hour)),
		Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

func flapMarker(source string) *observation.Observation {
	return flapValue(source, ibsignals.LinkFlapScanSince, observation.NodeTarget(""),
		&observation.Value{Kind: &observation.Value_IntValue{IntValue: flapStart.UnixMicro()}})
}

func flapTransition(source, cursor, state string, at time.Duration) []*observation.Observation {
	target := observation.JournalEntryTarget("boot", cursor)
	return []*observation.Observation{
		flapValue(source, ibsignals.LinkTransitionInterface, target, &observation.Value{Kind: &observation.Value_StringValue{StringValue: "enp90s0np0"}}),
		flapValue(source, ibsignals.LinkTransitionState, target, &observation.Value{Kind: &observation.Value_StringValue{StringValue: state}}),
		flapValue(source, ibsignals.LinkTransitionRealtime, target, &observation.Value{Kind: &observation.Value_IntValue{IntValue: flapStart.Add(at).UnixMicro()}}),
	}
}

func onlyLinkFlap(t *testing.T, measurements []health.Measurement) infiniband.LinkFlapMeasurement {
	t.Helper()
	if len(measurements) != 1 {
		t.Fatalf("got %d measurements, want 1: %v", len(measurements), measurements)
	}
	m, ok := measurements[0].(infiniband.LinkFlapMeasurement)
	if !ok {
		t.Fatalf("got %T, want infiniband.LinkFlapMeasurement", measurements[0])
	}
	return m
}

func linkFlapVerdict(t *testing.T, m health.Measurement) health.CheckVerdict {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	if err := cat.Register(infiniband.RDMALinkFlapFatal()); err != nil {
		t.Fatal(err)
	}
	v, err := cat.Evaluate("rdma_linkflap_fatal", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Skipped {
		t.Fatalf("rdma_linkflap_fatal skipped: %+v", v)
	}
	return v
}

func TestInfiniBandAdapter(t *testing.T) {
	a := InfiniBand()
	if a.Name() != "infiniband" || a.Category() != health.InfiniBand {
		t.Fatalf("name=%q category=%v", a.Name(), a.Category())
	}
	if got, want := slices.Sorted(maps.Keys(a.Signals())), slices.Sorted(slices.Values(ibsignals.LinkFlapSignals().IDs())); !slices.Equal(got, want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
	if a.nodeScoped {
		t.Fatal("the shared InfiniBand adapter must stay per entity for per-port types")
	}
	m := onlyLinkFlap(t, a.Build(slices.Concat(
		[]*observation.Observation{flapMarker("infiniband.journal")},
		flapTransition("infiniband.journal", "c1", ibsignals.LinkStateDown, 0),
		flapTransition("infiniband.journal", "c2", ibsignals.LinkStateUp, 45*time.Second),
	)))
	if len(m.Errors()) != 0 || !slices.Equal(m.OutageDurations, []int64{45}) {
		t.Fatalf("measurement = %+v errors %v", m, m.Errors())
	}
	if health.KeyOf(m) != health.NodeEntity() {
		t.Fatalf("link-flap measurement keyed %v, want the node", health.KeyOf(m))
	}
	if v := linkFlapVerdict(t, m); v.Severity != health.Critical {
		t.Fatalf("verdict = %v, want CRITICAL", v.Severity)
	}
}

// TestInfiniBandAdapterGroupsLinkFlapPerNode builds one measurement from
// several sources: grouping link-flap observations per source would split
// one scan into partial measurements that each fail the other's fields.
func TestInfiniBandAdapterGroupsLinkFlapPerNode(t *testing.T) {
	obs := slices.Concat(
		[]*observation.Observation{flapMarker("a")},
		flapTransition("b", "c1", ibsignals.LinkStateDown, 0),
		flapTransition("b", "c2", ibsignals.LinkStateUp, 10*time.Second),
	)
	m := onlyLinkFlap(t, InfiniBand().Build(obs))
	if len(m.Errors()) != 0 || !slices.Equal(m.OutageDurations, []int64{10}) {
		t.Fatalf("measurement = %+v errors %v", m, m.Errors())
	}

	failure := observation.NewCollectionErrorObservation(ibsignals.LinkTransitionInterface.ID(), ibsignals.LinkTransitionInterface.Unit(),
		"c", "ReadKernelBoot", nil, timestamppb.New(time.Unix(1, 0)), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable})
	m = onlyLinkFlap(t, InfiniBand().Build(append(obs, failure)))
	if len(m.Errors()) != 1 {
		t.Fatalf("source-scoped failure from another source: errors = %v, want one", m.Errors())
	}
}

// TestInfiniBandAdapterFailsSilentSource fails rdma_linkflap_fatal when the
// source emitted neither a scan marker nor a collection error, rather than
// leaving it without a measurement to skip.
func TestInfiniBandAdapterFailsSilentSource(t *testing.T) {
	for name, obs := range map[string][]*observation.Observation{
		"nothing": nil,
		"unrelated signals only": {flapValue("x", collect.NewSignal("infiniband.state_name", ""), observation.InfiniBandPortTarget("mlx5_0", 1),
			&observation.Value{Kind: &observation.Value_StringValue{StringValue: "ACTIVE"}})},
	} {
		t.Run(name, func(t *testing.T) {
			m := onlyLinkFlap(t, InfiniBand().Build(obs))
			if len(m.Errors()) != 1 || !strings.Contains(m.Errors()[0].Err().Error(), "no link-flap scan marker") {
				t.Fatalf("errors = %v", m.Errors())
			}
			if v := linkFlapVerdict(t, m); v.Severity != health.Critical {
				t.Fatalf("verdict = %v, want CRITICAL", v.Severity)
			}
		})
	}
}

// TestInfiniBandAdapterErroredCoversLinkFlap fails the link-flap check on a
// domain-wide failure such as a cycle timeout, with a retry recommendation.
func TestInfiniBandAdapterErroredCoversLinkFlap(t *testing.T) {
	m := onlyLinkFlap(t, InfiniBand().Errored(health.ErrCollectionTimeout))
	if len(m.Errors()) != 1 || !slices.Equal(m.Errors()[0].FailedSignals(), infiniband.MeasurementSignals()) {
		t.Fatalf("errors = %v", m.Errors())
	}
	v := linkFlapVerdict(t, m)
	if v.Severity != health.Critical || !v.RecommendedAction.Equal(health.Drain(health.RemediationActionRetryCollection)) {
		t.Fatalf("verdict = %v %v, want CRITICAL retry collection", v.Severity, v.RecommendedAction)
	}
	if !errors.Is(m.Errors()[0].Err(), health.ErrCollectionTimeout) {
		t.Fatal("timeout not preserved")
	}
}
