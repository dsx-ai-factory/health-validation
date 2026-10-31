// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"slices"
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

func ibPortValue(s collect.Signal, device string, port uint32, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "infiniband.sysfs", Target: observation.InfiniBandPortTarget(device, port),
		ObservedAt: timestamppb.New(time.Unix(1, 0)), Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

// ibPort returns the four observations the sensor emits for one port.
func ibPort(device string, state, phys int64, rate float64, layer string) []*observation.Observation {
	return []*observation.Observation{
		ibPortValue(ibsignals.State, device, 1, &observation.Value{Kind: &observation.Value_IntValue{IntValue: state}}),
		ibPortValue(ibsignals.PhysicalState, device, 1, &observation.Value{Kind: &observation.Value_IntValue{IntValue: phys}}),
		ibPortValue(ibsignals.RateGbps, device, 1, &observation.Value{Kind: &observation.Value_DoubleValue{DoubleValue: rate}}),
		ibPortValue(ibsignals.LinkLayer, device, 1, &observation.Value{Kind: &observation.Value_StringValue{StringValue: layer}}),
	}
}

func TestRDMAPortsSignalsAreSensorSignals(t *testing.T) {
	all := ibsignals.All().IDs()
	for id, catalogSignals := range RDMAPorts().Signals() {
		if !slices.Contains(all, id) {
			t.Errorf("adapter requests %q, which the infiniband sensor does not declare", id)
		}
		for _, s := range catalogSignals {
			if !slices.Contains(infiniband.PortSignals(), s) {
				t.Errorf("%q feeds %q, which PortMeasurement does not carry", id, s)
			}
		}
	}
}

func TestRDMAPortsBuild(t *testing.T) {
	var obs []*observation.Observation
	obs = append(obs, ibPort("mlx5_0", 4, 5, 400, "Ethernet")...)
	obs = append(obs, ibPort("mlx5_9", 1, 3, 400, "Ethernet")...)
	obs = append(obs, ibPort("mlx5_5", 4, 5, 100, "InfiniBand")...)

	got := RDMAPorts().Build(obs)
	if len(got) != 1 {
		t.Fatalf("Build returned %d measurements, want 1", len(got))
	}
	m, ok := got[0].(infiniband.PortMeasurement)
	if !ok {
		t.Fatalf("Build returned %T, want infiniband.PortMeasurement", got[0])
	}
	if len(m.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", m.Errors())
	}
	if m.States["mlx5_0/1"] != 4 || m.States["mlx5_9/1"] != 1 || m.PhysicalStates["mlx5_9/1"] != 3 {
		t.Errorf("states = %v / %v", m.States, m.PhysicalStates)
	}
	if m.RatesGbps["mlx5_5/1"] != 100 || m.LinkLayers["mlx5_0/1"] != "Ethernet" || m.LinkLayers["mlx5_5/1"] != "InfiniBand" {
		t.Errorf("rates = %v, layers = %v", m.RatesGbps, m.LinkLayers)
	}
}

// A port that could not be read is left out, so it is never counted as
// active; it does not fail the source.
func TestRDMAPortsPortFailureLeavesPortOut(t *testing.T) {
	obs := ibPort("mlx5_0", 4, 5, 400, "Ethernet")
	obs = append(obs, observation.NewCollectionErrorObservation(ibsignals.State.ID(), ibsignals.State.Unit(), "infiniband.sysfs", "mlx5_1/ports/1/state",
		observation.InfiniBandPortTarget("mlx5_1", 1), nil, &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}))

	m := RDMAPorts().Build(obs)[0].(infiniband.PortMeasurement)
	if len(m.Errors()) != 0 {
		t.Fatalf("a per-port failure became a measurement error: %v", m.Errors())
	}
	if _, ok := m.States["mlx5_1/1"]; ok {
		t.Errorf("unreadable port has a state: %v", m.States)
	}
}

// A failure that names no port is a failure of the whole source.
func TestRDMAPortsSourceFailure(t *testing.T) {
	obs := []*observation.Observation{
		observation.NewCollectionErrorObservation(ibsignals.State.ID(), ibsignals.State.Unit(), "infiniband.sysfs", "read", nil, nil,
			&observation.CollectionError{Category: observation.CollectionErrorCategorySourceError, Detail: "no such directory"}),
	}
	m := RDMAPorts().Build(obs)[0].(infiniband.PortMeasurement)
	if len(m.Errors()) != 1 || !slices.Contains(m.Errors()[0].FailedSignals(), infiniband.SignalPortStates) {
		t.Fatalf("errors = %v, want one on %s", m.Errors(), infiniband.SignalPortStates)
	}
}

func TestRDMAPortsNoObservationsIsErrored(t *testing.T) {
	got := RDMAPorts().Build(nil)
	if len(got) != 1 || len(got[0].Errors()) == 0 {
		t.Fatalf("Build(nil) = %v, want one errored measurement", got)
	}
}

// End to end through the catalog: the field case of three RoCE rails left
// down on an HGX B200 fails the check, eight up passes.
func TestRDMAPortsEvaluate(t *testing.T) {
	rails := func(downRails ...string) []*observation.Observation {
		var obs []*observation.Observation
		for _, d := range []string{"mlx5_0", "mlx5_1", "mlx5_2", "mlx5_3", "mlx5_4", "mlx5_9", "mlx5_12", "mlx5_13"} {
			state, phys := int64(4), int64(5)
			if slices.Contains(downRails, d) {
				state, phys = 1, 3
			}
			obs = append(obs, ibPort(d, state, phys, 400, "Ethernet")...)
		}
		// Front-end and storage ports below fabric speed.
		obs = append(obs, ibPort("mlx5_10", 4, 5, 200, "Ethernet")...)
		obs = append(obs, ibPort("mlx5_5", 4, 5, 100, "InfiniBand")...)
		return obs
	}
	cat := catalog.New(catalog.WithArchitecture("B200"), catalog.WithExecutionContext(health.OperatorInfra))
	for _, tc := range []struct {
		name string
		down []string
		want health.Severity
	}{
		{"all rails up", nil, health.Pass},
		{"three rails down", []string{"mlx5_9", "mlx5_12", "mlx5_13"}, health.Critical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := cat.Evaluate("rdma_fabric_ports_active", RDMAPorts().Build(rails(tc.down...))[0])
			if err != nil {
				t.Fatal(err)
			}
			if v.Severity != tc.want {
				t.Errorf("Severity = %v, want %v (%v)", v.Severity, tc.want, v.Messages)
			}
		})
	}
}

func TestRDMAPortsErrored(t *testing.T) {
	m := RDMAPorts().Errored(errors.New("boom"))
	if len(m) != 1 || m[0].Category() != health.InfiniBand || len(m[0].Errors()) == 0 {
		t.Fatalf("Errored = %v", m)
	}
}
