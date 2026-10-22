// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The adapter requests exactly the mapped port signals and builds one
// measurement per (device, port), in stable order, ignoring other domains.
func TestInfiniBandAdapterGroupsByPort(t *testing.T) {
	a := InfiniBand()
	if a.Name() != "infiniband" || a.Category() != health.InfiniBand {
		t.Fatalf("identity = %q/%v", a.Name(), a.Category())
	}
	if got, want := slices.Sorted(maps.Keys(a.Signals())), slices.Sorted(slices.Values(observations.MappedSignalsFor(health.InfiniBand))); !slices.Equal(got, want) {
		t.Fatalf("Signals = %v, want the mapped port signals %v", got, want)
	}
	obs := slices.Concat(portObservations("mlx5_1", 1, "Ethernet", 100), portObservations("mlx5_0", 1, "InfiniBand", 400))
	obs = append(obs, healthyGPUObservations()...)
	got := a.Build(obs)
	if len(got) != 2 {
		t.Fatalf("got %d measurements, want two ports: %v", len(got), got)
	}
	for i, want := range []infiniband.PortMeasurement{
		{Device: "mlx5_0", Port: 1, State: "ACTIVE", PhysicalState: "LinkUp", RateGbps: 400, LinkLayer: "InfiniBand"},
		{Device: "mlx5_1", Port: 1, State: "ACTIVE", PhysicalState: "LinkUp", RateGbps: 100, LinkLayer: "Ethernet"},
	} {
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("measurement %d = %+v (errors %v), want %+v", i, got[i], got[i].Errors(), want)
		}
	}
}

// No InfiniBand devices is absence, not a fault: nothing is emitted, so the
// port checks are skipped rather than failed.
func TestInfiniBandAdapterEmptyIsNeutral(t *testing.T) {
	if got := InfiniBand().Build(nil); len(got) != 0 {
		t.Fatalf("empty domain emitted %v, want nothing", got)
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
	for _, m := range InfiniBand().Build(obs) {
		byEntity[m.(infiniband.PortMeasurement).EntityID()] = m.(infiniband.PortMeasurement)
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

// A whole-source failure, and Errored, both yield one unidentified
// measurement with every signal failed, so no check passes on missing data.
func TestInfiniBandAdapterSourceFailure(t *testing.T) {
	o := portObservation(ibsignals.StateName, "", 0, nil)
	o.Target = nil
	o.Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}}
	for name, got := range map[string][]health.Measurement{
		"source failure": InfiniBand().Build([]*observation.Observation{o}),
		"errored":        InfiniBand().Errored(errors.New("timeout")),
	} {
		if len(got) != 1 {
			t.Fatalf("%s: got %d measurements, want one: %v", name, len(got), got)
		}
		port := got[0].(infiniband.PortMeasurement)
		if port.EntityID() != "" {
			t.Errorf("%s: EntityID = %q, want empty", name, port.EntityID())
		}
		if got, want := slices.Sorted(slices.Values(failed(port))), slices.Sorted(slices.Values(infiniband.PortMeasurementSignals)); !slices.Equal(got, want) {
			t.Errorf("%s: failed = %v, want every signal %v", name, got, want)
		}
	}
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
