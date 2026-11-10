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
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
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

// oneOf returns the one measurement of type M among measurements, which may
// also hold the category's other type.
func oneOf[M health.Measurement](t *testing.T, measurements []health.Measurement) M {
	t.Helper()
	var found []M
	for _, m := range measurements {
		if typed, ok := m.(M); ok {
			found = append(found, typed)
		}
	}
	if len(found) != 1 {
		var zero M
		t.Fatalf("got %d %T measurements, want 1: %v", len(found), zero, measurements)
	}
	return found[0]
}

// ports returns the port measurements among measurements, in order.
func ports(measurements []health.Measurement) []infiniband.PortMeasurement {
	var out []infiniband.PortMeasurement
	for _, m := range measurements {
		if port, ok := m.(infiniband.PortMeasurement); ok {
			out = append(out, port)
		}
	}
	return out
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

// The adapter requests every mapped InfiniBand signal and builds the
// link-flap measurement beside the ports.
func TestInfiniBandAdapter(t *testing.T) {
	a := InfiniBand()
	if a.Name() != "infiniband" || a.Category() != health.InfiniBand {
		t.Fatalf("name=%q category=%v", a.Name(), a.Category())
	}
	if got, want := slices.Sorted(maps.Keys(a.Signals())), slices.Sorted(slices.Values(observations.MappedSignalsFor(health.InfiniBand))); !slices.Equal(got, want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
	ms := a.Build(slices.Concat(
		[]*observation.Observation{flapMarker("infiniband.journal")},
		flapTransition("infiniband.journal", "c1", ibsignals.LinkStateDown, 0),
		flapTransition("infiniband.journal", "c2", ibsignals.LinkStateUp, 45*time.Second),
		portObservations("mlx5_0", 1, "InfiniBand", 400),
	))
	m := oneOf[infiniband.LinkFlapMeasurement](t, ms)
	if len(m.Errors()) != 0 || !slices.Equal(m.OutageDurations, []int64{45}) {
		t.Fatalf("measurement = %+v errors %v", m, m.Errors())
	}
	if health.KeyOf(m) != health.NodeEntity() {
		t.Fatalf("link-flap measurement keyed %v, want the node", health.KeyOf(m))
	}
	if v := linkFlapVerdict(t, m); v.Severity != health.Critical {
		t.Fatalf("verdict = %v, want CRITICAL", v.Severity)
	}
	if port := oneOf[infiniband.PortMeasurement](t, ms); len(port.Errors()) != 0 || port.EntityID() != "mlx5_0/1" {
		t.Fatalf("port = %+v errors %v", port, port.Errors())
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
	m := oneOf[infiniband.LinkFlapMeasurement](t, InfiniBand().Build(obs))
	if len(m.Errors()) != 0 || !slices.Equal(m.OutageDurations, []int64{10}) {
		t.Fatalf("measurement = %+v errors %v", m, m.Errors())
	}

	failure := observation.NewCollectionErrorObservation(ibsignals.LinkTransitionInterface.ID(), ibsignals.LinkTransitionInterface.Unit(),
		"c", "ReadKernelBoot", nil, timestamppb.New(time.Unix(1, 0)), &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable})
	m = oneOf[infiniband.LinkFlapMeasurement](t, InfiniBand().Build(append(obs, failure)))
	if len(m.Errors()) != 1 {
		t.Fatalf("source-scoped failure from another source: errors = %v, want one", m.Errors())
	}
}

// TestInfiniBandAdapterFailsSilentSource fails rdma_linkflap_fatal when the
// source emitted neither a scan marker nor a collection error, rather than
// leaving it without a measurement to skip.
func TestInfiniBandAdapterFailsSilentSource(t *testing.T) {
	for name, obs := range map[string][]*observation.Observation{
		"nothing":                nil,
		"unrelated signals only": healthyGPUObservations()[:1],
	} {
		t.Run(name, func(t *testing.T) {
			m := oneOf[infiniband.LinkFlapMeasurement](t, InfiniBand().Build(obs))
			if len(m.Errors()) != 1 || !strings.Contains(m.Errors()[0].Err().Error(), "no link-flap scan marker") {
				t.Fatalf("errors = %v", m.Errors())
			}
			if v := linkFlapVerdict(t, m); v.Severity != health.Unknown {
				t.Fatalf("verdict = %v, want UNKNOWN", v.Severity)
			}
		})
	}
}

// TestInfiniBandAdapterErroredCoversLinkFlap fails the link-flap check on a
// domain-wide failure such as a cycle timeout, with a retry recommendation.
func TestInfiniBandAdapterErroredCoversLinkFlap(t *testing.T) {
	m := oneOf[infiniband.LinkFlapMeasurement](t, InfiniBand().Errored(health.ErrCollectionTimeout))
	if len(m.Errors()) != 1 || !slices.Equal(m.Errors()[0].FailedSignals(), infiniband.MeasurementSignals()) {
		t.Fatalf("errors = %v", m.Errors())
	}
	v := linkFlapVerdict(t, m)
	if v.Severity != health.Unknown || !v.RecommendedAction.Equal(health.Drain(health.RemediationActionRetryCollection)) {
		t.Fatalf("verdict = %v %v, want UNKNOWN retry collection", v.Severity, v.RecommendedAction)
	}
	if !errors.Is(m.Errors()[0].Err(), health.ErrCollectionTimeout) {
		t.Fatal("timeout not preserved")
	}
}

// The adapter builds one measurement per (device, port), in stable order,
// ignoring other domains.
func TestInfiniBandAdapterGroupsByPort(t *testing.T) {
	obs := slices.Concat(portObservations("mlx5_1", 1, "Ethernet", 100), portObservations("mlx5_0", 1, "InfiniBand", 400))
	obs = append(obs, healthyGPUObservations()...)
	got := ports(InfiniBand().Build(obs))
	if len(got) != 2 {
		t.Fatalf("got %d port measurements, want two: %v", len(got), got)
	}
	for i, want := range []infiniband.PortMeasurement{
		{Device: "mlx5_0", Port: 1, State: "ACTIVE", PhysicalState: "LinkUp", RateGbps: 400, LinkLayer: "InfiniBand"},
		{Device: "mlx5_1", Port: 1, State: "ACTIVE", PhysicalState: "LinkUp", RateGbps: 100, LinkLayer: "Ethernet"},
	} {
		port := got[i]
		if port.Device != want.Device || port.Port != want.Port || port.State != want.State ||
			port.PhysicalState != want.PhysicalState || port.RateGbps != want.RateGbps || port.LinkLayer != want.LinkLayer {
			t.Errorf("measurement %d = %+v, want %+v", i, port, want)
		}
		if len(port.Errors()) != 0 {
			t.Errorf("measurement %d errors = %v, want none", i, port.Errors())
		}
		// The converter records the key it grouped by, the port's verified ID.
		if key := health.KeyOf(port); key != health.VerifiedEntity(want.EntityID()) {
			t.Errorf("measurement %d keyed %v, want %v", i, key, health.VerifiedEntity(want.EntityID()))
		}
	}
}

// A failure listing a device's ports names the device and fails every port
// signal; a per-signal failure fails only that signal.
func TestInfiniBandAdapterAttributesFailures(t *testing.T) {
	unreadable := &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, Detail: "permission denied"}
	var obs []*observation.Observation
	for _, s := range []collect.Signal{ibsignals.StateName, ibsignals.PhysicalStateName, ibsignals.RateGbps, ibsignals.LinkLayer} {
		o := portObservation(s, "mlx5_3", 0, nil)
		o.Target = observation.InfiniBandDeviceTarget("mlx5_3")
		o.Outcome = &observation.Observation_CollectionError{CollectionError: unreadable}
		obs = append(obs, o)
	}
	rateFailed := portObservation(ibsignals.RateGbps, "mlx5_0", 1, nil)
	rateFailed.Outcome = &observation.Observation_CollectionError{CollectionError: unreadable}
	obs = append(obs, replace(portObservations("mlx5_0", 1, "InfiniBand", 400), ibsignals.RateGbps.ID(), rateFailed)...)

	byEntity := make(map[string]infiniband.PortMeasurement)
	for _, m := range ports(InfiniBand().Build(obs)) {
		byEntity[m.EntityID()] = m
	}
	if len(byEntity) != 2 {
		t.Fatalf("got %v, want the port and the device", byEntity)
	}
	port, device := byEntity["mlx5_0/1"], byEntity["mlx5_3"]
	if !slices.Equal(failed(port), []string{infiniband.SignalIBRateGbps}) {
		t.Errorf("port failed %v, want only the rate failed", failed(port))
	}
	if device.Device != "mlx5_3" || device.Port != 0 {
		t.Errorf("device-level measurement = %+v, want mlx5_3 with no port", device)
	}
	for _, s := range []string{infiniband.SignalIBState, infiniband.SignalIBPhysicalState, infiniband.SignalIBRateGbps, infiniband.SignalIBLinkLayer} {
		if !slices.Contains(failed(device), s) {
			t.Errorf("device-level failure left %s unfailed: %v", s, failed(device))
		}
	}
}

// A whole-source failure, no observations at all, and Errored each yield one
// source-keyed port measurement with every signal failed, so no check passes
// on missing data. Zero ports is not evidence of a host without InfiniBand: a
// missing driver or the device filter produces the same emptiness.
func TestInfiniBandAdapterSourceFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  []health.Measurement
		want health.EntityKey
	}{
		{name: "source failure", got: InfiniBand().Build([]*observation.Observation{sourceFailure()}), want: health.SourceEntity("infiniband.sysfs")},
		{name: "no observations", got: InfiniBand().Build(nil), want: health.SourceEntity("collect.infiniband")},
		{name: "errored", got: InfiniBand().Errored(errors.New("timeout")), want: health.SourceEntity("collect.infiniband")},
	} {
		port := oneOf[infiniband.PortMeasurement](t, tc.got)
		if port.EntityID() != "" {
			t.Errorf("%s: EntityID = %q, want empty", tc.name, port.EntityID())
		}
		if key := health.KeyOf(port); key != tc.want {
			t.Errorf("%s: key = %v, want %v", tc.name, key, tc.want)
		}
		if got, want := slices.Sorted(slices.Values(failed(port))), slices.Sorted(slices.Values(infiniband.PortMeasurementSignals)); !slices.Equal(got, want) {
			t.Errorf("%s: failed = %v, want every signal %v", tc.name, got, want)
		}
	}
}

// Two healthy ports beside a failure to enumerate ports are three entities:
// each port passes under its own name and the failure reports UNKNOWN
// without naming one.
func TestInfiniBandAdapterDistinguishesPortsFromEnumerationFailure(t *testing.T) {
	obs := slices.Concat(portObservations("mlx5_0", 1, "InfiniBand", 400), portObservations("mlx5_1", 1, "InfiniBand", 400))
	obs = append(obs, sourceFailure())
	ms := InfiniBand().Build(obs)
	var keys []health.EntityKey
	for _, m := range ports(ms) {
		keys = append(keys, health.KeyOf(m))
	}
	want := []health.EntityKey{health.VerifiedEntity("mlx5_0/1"), health.VerifiedEntity("mlx5_1/1"), health.SourceEntity("infiniband.sysfs")}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}

	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	verdict, err := cat.EvaluateAll(ms...)
	if err != nil {
		t.Fatalf("EvaluateAll: %v, want the three entities told apart", err)
	}
	var passed, unknown []string
	for _, v := range verdict.CheckResults {
		if v.CheckID != "ib_port_state" || v.Skipped {
			continue
		}
		switch v.Severity {
		case health.Pass:
			passed = append(passed, v.AffectedEntities...)
			passed = append(passed, "pass")
		case health.Unknown:
			unknown = append(unknown, v.AffectedEntities...)
			unknown = append(unknown, "unknown")
		}
	}
	if len(passed) != 2 || len(unknown) != 1 {
		t.Fatalf("ib_port_state results: passed %v, unknown %v; want two passes and one unnamed failure: %+v", passed, unknown, verdict.CheckResults)
	}
}

// With the legacy link-flap sensor in place, the ports-only adapter neither
// requests link-flap signals nor fails that type, which would duplicate the
// legacy measurement.
func TestInfiniBandPortsAdapterLeavesLinkFlapToLegacySensor(t *testing.T) {
	a := InfiniBandPorts()
	linkFlap := ibsignals.LinkFlapSignals().IDs()
	want := slices.DeleteFunc(observations.MappedSignalsFor(health.InfiniBand), func(id string) bool { return slices.Contains(linkFlap, id) })
	if got := slices.Sorted(maps.Keys(a.Signals())); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Fatalf("signals = %v, want the port signals %v", got, want)
	}
	for name, ms := range map[string][]health.Measurement{
		"no observations": a.Build(nil),
		"errored":         a.Errored(errors.New("timeout")),
	} {
		if len(ms) != 1 {
			t.Fatalf("%s: got %d measurements, want one port failure: %v", name, len(ms), ms)
		}
		if key := health.KeyOf(oneOf[infiniband.PortMeasurement](t, ms)); key != health.SourceEntity("collect.infiniband") {
			t.Errorf("%s: key = %v, want the adapter", name, key)
		}
	}
	if got := a.Build(portObservations("mlx5_0", 1, "InfiniBand", 400)); len(got) != 1 || oneOf[infiniband.PortMeasurement](t, got).EntityID() != "mlx5_0/1" {
		t.Fatalf("Build = %v, want the one port", got)
	}
}

// sourceFailure is a target-less collection failure: no port was enumerated.
func sourceFailure() *observation.Observation {
	o := portObservation(ibsignals.StateName, "", 0, nil)
	o.Target = nil
	o.Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}}
	return o
}

func failed(m health.Measurement) []string {
	var out []string
	for _, e := range m.Errors() {
		for _, s := range e.FailedSignals() {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func portObservations(device string, port uint32, linkLayer string, rate float64) []*observation.Observation {
	return []*observation.Observation{
		portObservation(ibsignals.StateName, device, port, stringValue("ACTIVE")),
		portObservation(ibsignals.PhysicalStateName, device, port, stringValue("LinkUp")),
		portObservation(ibsignals.RateGbps, device, port, doubleValue(rate)),
		portObservation(ibsignals.LinkLayer, device, port, stringValue(linkLayer)),
	}
}

func portObservation(s collect.Signal, device string, port uint32, value *observation.Value) *observation.Observation {
	o := &observation.Observation{
		SignalId:   s.ID(),
		Source:     "infiniband.sysfs",
		Target:     observation.InfiniBandPortTarget(device, port),
		ObservedAt: timestamppb.Now(),
		Unit:       s.Unit(),
	}
	if value != nil {
		o.Outcome = &observation.Observation_Value{Value: value}
	}
	return o
}

func stringValue(v string) *observation.Value {
	return &observation.Value{Kind: &observation.Value_StringValue{StringValue: v}}
}
