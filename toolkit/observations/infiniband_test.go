// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Two measurement types share the infiniband domain: journal link flaps and
// sysfs ports. One batch feeds both, and ports are grouped by (device, port).
func TestToMeasurementsSeparatesInfiniBandTypes(t *testing.T) {
	obs := slices.Concat(
		healthyPortObservations("mlx5_1", 1),
		healthyPortObservations("mlx5_0", 2),
		healthyPortObservations("mlx5_0", 1),
	)
	flap := valueObservation("infiniband."+infiniband.SignalOutageDurations, stringValue("[45]"))
	flap.Target = observation.NodeTarget("")
	obs = append(obs, flap)

	measurements := ToMeasurements(obs)
	if len(measurements) != 4 {
		t.Fatalf("got %d measurements, want link flaps plus three ports: %v", len(measurements), measurements)
	}
	flaps, ok := measurements[0].(infiniband.LinkFlapMeasurement)
	if !ok || !slices.Equal(flaps.OutageDurations, []int64{45}) {
		t.Fatalf("measurements[0] = %#v, want the link-flap measurement", measurements[0])
	}
	var ids []string
	for _, m := range measurements[1:] {
		port, ok := m.(infiniband.PortMeasurement)
		if !ok {
			t.Fatalf("measurement %#v is not a port", m)
		}
		if len(port.Errors()) != 0 {
			t.Errorf("%s: errors = %v, want none", port.EntityID(), port.Errors())
		}
		if port.State != "ACTIVE" || port.PhysicalState != "LinkUp" || port.RateGbps != 400 || port.LinkLayer != "InfiniBand" {
			t.Errorf("%s: fields = %+v", port.EntityID(), port)
		}
		ids = append(ids, port.EntityID())
	}
	if want := []string{"mlx5_0/1", "mlx5_0/2", "mlx5_1/1"}; !slices.Equal(ids, want) {
		t.Errorf("ports = %v, want %v in stable order", ids, want)
	}
}

// A failure listing a device's ports arrives with a device target: the
// measurement names the device, and every port signal fails.
func TestToMeasurementsInfiniBandDeviceFailure(t *testing.T) {
	var obs []*observation.Observation
	for _, s := range portSignals() {
		o := ibObservation(s, nil)
		o.Target = observation.InfiniBandDeviceTarget("mlx5_3")
		o.Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, Detail: "ports: permission denied"}}
		obs = append(obs, o)
	}
	port := only[infiniband.PortMeasurement](t, ToMeasurements(obs))
	if port.EntityID() != "mlx5_3" || port.Port != 0 {
		t.Fatalf("identity = %q/%d, want the device alone", port.Device, port.Port)
	}
	failed := failedSignals(port)
	for _, s := range []string{infiniband.SignalIBState, infiniband.SignalIBPhysicalState, infiniband.SignalIBRateGbps, infiniband.SignalIBLinkLayer} {
		if !slices.Contains(failed, s) {
			t.Errorf("%s not failed: %v", s, failed)
		}
	}
	for _, s := range []string{infiniband.SignalIBDevice, infiniband.SignalIBPort} {
		if slices.Contains(failed, s) {
			t.Errorf("identity %s failed although the target named it: %v", s, failed)
		}
	}
}

// A whole-source failure carries no target and yields one unidentified
// measurement, which EntityID omits, with every signal failed.
func TestToMeasurementsInfiniBandSourceFailure(t *testing.T) {
	o := ibObservation(ibsignals.StateName, nil)
	o.Target = nil
	o.Outcome = &observation.Observation_CollectionError{CollectionError: &observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable}}
	port := only[infiniband.PortMeasurement](t, ToMeasurements([]*observation.Observation{o}))
	if port.EntityID() != "" {
		t.Fatalf("EntityID = %q, want empty for an unidentified port", port.EntityID())
	}
	if failed := failedSignals(port); !slices.Equal(failed, []string{infiniband.SignalIBState}) {
		t.Errorf("failed = %v, want the observed signal", failed)
	}
	if missing := missingSignals(port); !slices.Contains(missing, infiniband.SignalIBDevice) || !slices.Contains(missing, infiniband.SignalIBPort) {
		t.Errorf("missing = %v, want the identity reported as never observed", missing)
	}
}

// A value must say which port it describes.
func TestToMeasurementsInfiniBandValueRequiresTarget(t *testing.T) {
	o := ibObservation(ibsignals.StateName, stringValue("ACTIVE"))
	o.Target = nil
	port := only[infiniband.PortMeasurement](t, ToMeasurements([]*observation.Observation{o}))
	if failed := failedSignals(port); !slices.Contains(failed, infiniband.SignalIBState) || !slices.Contains(failed, infiniband.SignalIBPort) {
		t.Errorf("failed = %v, want the value and the identity failed", failed)
	}
}

// The rate must arrive in the canonical unit; a mismatched unit fails the
// signal rather than being read as a different magnitude.
func TestToMeasurementsInfiniBandRateUnit(t *testing.T) {
	o := ibObservation(ibsignals.RateGbps, doubleValue(400))
	wrong := "bytes_per_second"
	o.Unit = &wrong
	port := only[infiniband.PortMeasurement](t, ToMeasurements([]*observation.Observation{o}))
	if failed := failedSignals(port); !slices.Equal(failed, []string{infiniband.SignalIBRateGbps}) {
		t.Errorf("failed = %v, want only the rate", failed)
	}
}

// A port measurement exports with its port target, a device-level one with
// a device target, and an unidentified or invalid one with none.
func TestFromMeasurementsTargetsInfiniBandPorts(t *testing.T) {
	for _, tc := range []struct {
		m    health.Measurement
		want *observation.ObservationTarget
		name string
	}{
		{name: "port", m: infiniband.PortMeasurement{Device: "mlx5_0", Port: 1}, want: observation.InfiniBandPortTarget("mlx5_0", 1)},
		{name: "device", m: infiniband.PortMeasurement{Device: "mlx5_0"}, want: observation.InfiniBandDeviceTarget("mlx5_0")},
		{name: "unidentified", m: infiniband.PortMeasurement{}},
		{name: "invalid port", m: infiniband.PortMeasurement{Device: "mlx5_0", Port: -1}},
	} {
		for id, o := range exportBySignal(t, tc.m) {
			if got := o.GetTarget().String(); got != tc.want.String() {
				t.Errorf("%s: %s target = %v, want %v", tc.name, id, o.GetTarget(), tc.want)
			}
		}
	}
}

func portSignals() []collect.Signal {
	return []collect.Signal{ibsignals.StateName, ibsignals.PhysicalStateName, ibsignals.RateGbps, ibsignals.LinkLayer}
}

func healthyPortObservations(device string, port uint32) []*observation.Observation {
	values := map[string]*observation.Value{
		ibsignals.StateName.ID():         stringValue("ACTIVE"),
		ibsignals.PhysicalStateName.ID(): stringValue("LinkUp"),
		ibsignals.RateGbps.ID():          doubleValue(400),
		ibsignals.LinkLayer.ID():         stringValue("InfiniBand"),
	}
	var out []*observation.Observation
	for _, s := range portSignals() {
		o := ibObservation(s, values[s.ID()])
		o.Target = observation.InfiniBandPortTarget(device, port)
		out = append(out, o)
	}
	return out
}

// ibObservation is a sysfs-sourced observation of s for mlx5_0 port 1; a
// nil value leaves the outcome for the caller to set.
func ibObservation(s collect.Signal, value *observation.Value) *observation.Observation {
	o := &observation.Observation{
		SignalId:   s.ID(),
		Source:     "infiniband.sysfs",
		Target:     observation.InfiniBandPortTarget("mlx5_0", 1),
		ObservedAt: timestamppb.Now(),
		Unit:       s.Unit(),
	}
	if value != nil {
		o.Outcome = &observation.Observation_Value{Value: value}
	}
	return o
}
