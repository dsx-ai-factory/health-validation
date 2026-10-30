// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var flapWindow = time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)

func flapValue(s collect.Signal, target *observation.ObservationTarget, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "infiniband.journal", Target: target, ObservedAt: timestamppb.Now(),
		Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

func flapMarker() *observation.Observation {
	return flapValue(ibsignals.LinkFlapScanSince, observation.NodeTarget(""),
		&observation.Value{Kind: &observation.Value_IntValue{IntValue: flapWindow.UnixMicro()}})
}

// flapEvent is one transition's three observations on journal entry cursor.
type flapEvent struct {
	iface  string
	state  string
	cursor string
	at     time.Duration // after flapWindow
}

func (e flapEvent) observations() []*observation.Observation {
	target := observation.JournalEntryTarget("boot", e.cursor)
	return []*observation.Observation{
		flapValue(ibsignals.LinkTransitionInterface, target, &observation.Value{Kind: &observation.Value_StringValue{StringValue: e.iface}}),
		flapValue(ibsignals.LinkTransitionState, target, &observation.Value{Kind: &observation.Value_StringValue{StringValue: e.state}}),
		flapValue(ibsignals.LinkTransitionRealtime, target, &observation.Value{Kind: &observation.Value_IntValue{IntValue: flapWindow.Add(e.at).UnixMicro()}}),
	}
}

func flaps(events ...flapEvent) []*observation.Observation {
	out := []*observation.Observation{flapMarker()}
	for i, e := range events {
		if e.cursor == "" {
			e.cursor = fmt.Sprintf("c%d", i+1)
		}
		out = append(out, e.observations()...)
	}
	return out
}

func down(iface string, at time.Duration) flapEvent {
	return flapEvent{iface: iface, state: ibsignals.LinkStateDown, at: at}
}

func up(iface string, at time.Duration) flapEvent {
	return flapEvent{iface: iface, state: ibsignals.LinkStateUp, at: at}
}

func onlyLinkFlap(t *testing.T, measurements []health.Measurement) infiniband.LinkFlapMeasurement {
	t.Helper()
	var out []infiniband.LinkFlapMeasurement
	for _, m := range measurements {
		if typed, ok := m.(infiniband.LinkFlapMeasurement); ok {
			out = append(out, typed)
		}
	}
	if len(out) != 1 {
		t.Fatalf("got %d link-flap measurements, want 1: %v", len(out), measurements)
	}
	return out[0]
}

func flapTime(at time.Duration) string {
	return journalTime(flapWindow.Add(at).UnixMicro()).Format(linkFlapTimeLayout)
}

func TestToMeasurementsPairsLinkTransitions(t *testing.T) {
	tests := []struct {
		name      string
		events    []flapEvent
		durations []int64
		flaps     []infiniband.LinkFlapEvent
	}{
		{name: "clean scan"},
		{name: "only up", events: []flapEvent{up("ib0", 0)}},
		{name: "unrecovered down", events: []flapEvent{down("ib0", 0)}},
		{
			name:      "one outage",
			events:    []flapEvent{down("ib0", time.Minute), up("ib0", 2*time.Minute+54*time.Second)},
			durations: []int64{114},
			flaps:     []infiniband.LinkFlapEvent{{Interface: "ib0", DownAt: flapTime(time.Minute), UpAt: flapTime(2*time.Minute + 54*time.Second), DurationSeconds: 114}},
		},
		{
			name:      "consecutive downs collapse from the first",
			events:    []flapEvent{down("ib0", 0), down("ib0", 30*time.Second), up("ib0", 2*time.Minute)},
			durations: []int64{120},
			flaps:     []infiniband.LinkFlapEvent{{Interface: "ib0", DownAt: flapTime(0), UpAt: flapTime(2 * time.Minute), DurationSeconds: 120}},
		},
		{
			name:      "interfaces pair independently in name order",
			events:    []flapEvent{down("ib1", 0), down("ib0", 0), up("ib1", 5*time.Second), up("ib0", 40*time.Second)},
			durations: []int64{40, 5},
		},
		{
			name:      "report order is not time order",
			events:    []flapEvent{up("ib0", time.Minute), down("ib0", 0)},
			durations: []int64{60},
		},
		{
			// Second precision: 0.9s → 1.1s spans one second boundary.
			name:      "durations count whole seconds crossed",
			events:    []flapEvent{down("ib0", 900*time.Millisecond), up("ib0", 1100*time.Millisecond)},
			durations: []int64{1},
		},
		{
			// Within one second, report order decides: down then up pairs.
			name:      "same second in report order",
			events:    []flapEvent{down("ib0", 100*time.Millisecond), up("ib0", 50*time.Millisecond)},
			durations: []int64{0},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := onlyLinkFlap(t, ToMeasurements(flaps(test.events...)))
			if len(m.Errors()) != 0 {
				t.Fatalf("errors = %v", m.Errors())
			}
			if m.Since != flapTime(0) {
				t.Errorf("since = %q, want %q", m.Since, flapTime(0))
			}
			if !slices.Equal(m.OutageDurations, test.durations) {
				t.Errorf("durations = %v, want %v", m.OutageDurations, test.durations)
			}
			if test.flaps != nil && !slices.Equal(m.FlapEvents, test.flaps) {
				t.Errorf("flap events = %+v, want %+v", m.FlapEvents, test.flaps)
			}
			if len(m.FlapEvents) != len(m.OutageDurations) {
				t.Errorf("flap events = %v for durations %v", m.FlapEvents, m.OutageDurations)
			}
		})
	}
}

// TestToMeasurementsLinkFlapFailsClosed fails every link-flap signal when
// any part of the scan is missing, failed, or malformed, even beside a
// successful marker.
func TestToMeasurementsLinkFlapFailsClosed(t *testing.T) {
	withoutSignal := func(obs []*observation.Observation, s collect.Signal) []*observation.Observation {
		return slices.DeleteFunc(obs, func(o *observation.Observation) bool { return o.GetSignalId() == s.ID() })
	}
	parseFailure := observation.NewCollectionErrorObservation(ibsignals.LinkTransitionInterface.ID(), ibsignals.LinkTransitionInterface.Unit(),
		"infiniband.journal", "parse", observation.JournalEntryTarget("boot", "bad"), timestamppb.Now(),
		&observation.CollectionError{Category: observation.CollectionErrorCategoryParseError, SourceError: ibsignals.ReasonMissingInterface})
	timeout := observation.NewCollectionErrorObservation(ibsignals.LinkFlapScanSince.ID(), ibsignals.LinkFlapScanSince.Unit(),
		"infiniband.journal", "Measure", nil, timestamppb.Now(),
		&observation.CollectionError{Category: observation.CollectionErrorCategoryTimeout})
	badState := flaps(flapEvent{iface: "ib0", state: "flapping", cursor: "c1"})
	wrongKind := flaps(down("ib0", 0))
	wrongKind[len(wrongKind)-1] = flapValue(ibsignals.LinkTransitionRealtime, observation.JournalEntryTarget("boot", "c1"),
		&observation.Value{Kind: &observation.Value_StringValue{StringValue: "now"}})

	untargeted := flaps(down("ib0", 0))
	untargeted[1].Target = nil
	duplicateField := append(flaps(down("ib0", 0)), flapEvent{iface: "ib1", state: ibsignals.LinkStateDown, cursor: "c1"}.observations()[0])
	badInterface := flaps(flapEvent{iface: "a/b", state: ibsignals.LinkStateDown, cursor: "c1"})

	tests := []struct {
		name    string
		since   string
		obs     []*observation.Observation
		timeout bool
	}{
		{name: "two markers", obs: append(flaps(), flapMarker()), since: linkFlapSinceUnknown},
		{name: "window starts after the scan", obs: func() []*observation.Observation {
			obs := flaps()
			obs[0].ObservedAt = timestamppb.New(flapWindow.Add(-time.Second))
			return obs
		}(), since: linkFlapSinceUnknown},
		{name: "transition without a journal entry", obs: untargeted, since: flapTime(0)},
		{name: "field observed twice for one entry", obs: duplicateField, since: flapTime(0)},
		{name: "transition before the window", obs: flaps(down("ib0", -time.Second), up("ib0", time.Minute)), since: flapTime(0)},
		{name: "invalid interface", obs: badInterface, since: flapTime(0)},
		{name: "marker missing", obs: flaps(down("ib0", 0), up("ib0", time.Minute))[1:], since: linkFlapSinceUnknown},
		{name: "event parse error beside marker", obs: append(flaps(down("ib0", 0), up("ib0", time.Minute)), parseFailure), since: flapTime(0)},
		{name: "transition without state", obs: withoutSignal(flaps(down("ib0", 0)), ibsignals.LinkTransitionState), since: flapTime(0)},
		{name: "transition without interface", obs: withoutSignal(flaps(down("ib0", 0)), ibsignals.LinkTransitionInterface), since: flapTime(0)},
		{name: "transition without time", obs: withoutSignal(flaps(down("ib0", 0)), ibsignals.LinkTransitionRealtime), since: flapTime(0)},
		{name: "unknown state", obs: badState, since: flapTime(0)},
		{name: "wrong value kind", obs: wrongKind, since: flapTime(0)},
		{name: "timeout", obs: []*observation.Observation{timeout}, since: linkFlapSinceUnknown, timeout: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := onlyLinkFlap(t, ToMeasurements(test.obs))
			if len(m.Errors()) != 1 || !slices.Equal(m.Errors()[0].FailedSignals(), infiniband.MeasurementSignals()) {
				t.Fatalf("errors = %+v, want one naming every link-flap signal", m.Errors())
			}
			if m.OutageDurations != nil || m.FlapEvents != nil || m.Since != test.since {
				t.Errorf("measurement = %+v, want empty since %q", m, test.since)
			}
			if got := errors.Is(m.Errors()[0].Err(), health.ErrCollectionTimeout); got != test.timeout {
				t.Errorf("timeout = %v, want %v", got, test.timeout)
			}
		})
	}
}

func TestToMeasurementsRejectsMixedLinkFlapForms(t *testing.T) {
	field := &observation.Observation{
		SignalId: "infiniband.since", Source: "toolkit.infiniband", Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: "boot"}}},
	}
	m := onlyLinkFlap(t, ToMeasurements(append(flaps(), field)))
	if len(m.Errors()) != 1 || !slices.Equal(m.Errors()[0].FailedSignals(), infiniband.MeasurementSignals()) {
		t.Fatalf("mixed link-flap forms = %+v, want one errored measurement", m)
	}
}
