// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"reflect"
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
)

func journalValue(s collect.Signal, cursor string, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "xid.journal", Target: observation.JournalEntryTarget("boot", cursor),
		ObservedAt: timestamppb.Now(), Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

// journalXid is one canonical Xid event, as the journal sensor emits it.
func journalXid(cursor string, code int64, pci string) []*observation.Observation {
	return []*observation.Observation{
		journalValue(xidsignals.Code, cursor, &observation.Value{Kind: &observation.Value_IntValue{IntValue: code}}),
		journalValue(xidsignals.PCIAddress, cursor, &observation.Value{Kind: &observation.Value_StringValue{StringValue: pci}}),
	}
}

func journalMarker() *observation.Observation {
	o := journalValue(xidsignals.ScanWindow, "", &observation.Value{Kind: &observation.Value_StringValue{StringValue: xidsignals.WindowBoot}})
	o.Target = observation.NodeTarget("")
	return o
}

func journalBatch(events ...[]*observation.Observation) []*observation.Observation {
	return append(slices.Concat(events...), journalMarker())
}

func xidMeasurements(t *testing.T, measurements []health.Measurement) []xid.XidMeasurement {
	t.Helper()
	var out []xid.XidMeasurement
	for _, m := range measurements {
		x, ok := m.(xid.XidMeasurement)
		if !ok {
			t.Fatalf("got %T, want only Xid measurements", m)
		}
		out = append(out, x)
	}
	return out
}

// xidSummary is what a test compares: identity, counts and events, no errors.
type xidSummary struct {
	XidCounts  map[int64]int64
	GPUID      string
	PCIAddress string
	Events     []xid.XidEvent
	EventCount int64
}

func summarize(t *testing.T, measurements []health.Measurement) []xidSummary {
	t.Helper()
	var out []xidSummary
	for _, m := range xidMeasurements(t, measurements) {
		if len(m.Errors()) != 0 {
			t.Fatalf("unexpected errors: %v", m.Errors())
		}
		out = append(out, xidSummary{m.XidCounts, m.GPUID, m.PCIAddress, m.Events, m.EventCount})
	}
	return out
}

func TestToMeasurementsSplitsXidPerGPU(t *testing.T) {
	batch := journalBatch(journalXid("a", 48, "0000:89:00"), journalXid("b", 13, "0000:c1:00"), journalXid("c", 48, "0000:89:00"))
	want := []xidSummary{
		{XidCounts: map[int64]int64{48: 2}, PCIAddress: "0000:89:00", EventCount: 2, Events: []xid.XidEvent{
			{XidCode: 48, PCIAddress: "0000:89:00"}, {XidCode: 48, PCIAddress: "0000:89:00"},
		}},
		{XidCounts: map[int64]int64{13: 1}, PCIAddress: "0000:c1:00", EventCount: 1, Events: []xid.XidEvent{
			{XidCode: 13, PCIAddress: "0000:c1:00"},
		}},
	}
	if got := summarize(t, ToMeasurements(batch)); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestToMeasurementsXidNamesGPUFromUUIDs(t *testing.T) {
	batch := journalBatch(journalXid("a", 48, "0000:89:00"), journalXid("b", 48, "0000:c1:00"))
	got := summarize(t, ToMeasurements(batch, WithGPUUUIDs(map[string]string{"0000:89:00": "GPU-a"})))
	if len(got) != 2 || got[0].GPUID != "GPU-a" || got[1].GPUID != "" {
		t.Fatalf("got %+v, want GPU-a on 0000:89:00 and no UUID on 0000:c1:00", got)
	}
}

func TestToMeasurementsXidNormalizesAddressSpelling(t *testing.T) {
	batch := journalBatch(journalXid("a", 48, "0000:C1:00"), journalXid("b", 48, "0000:c1:00.0"))
	got := summarize(t, ToMeasurements(batch, WithGPUUUIDs(map[string]string{"0000:c1:00": "GPU-a"})))
	if len(got) != 1 || got[0].GPUID != "GPU-a" || got[0].PCIAddress != "0000:c1:00" || got[0].XidCounts[48] != 2 {
		t.Fatalf("got %+v, want one GPU-a measurement at 0000:c1:00 with two Xid 48s", got)
	}
}

func TestToMeasurementsXidCleanScanHasNoIdentity(t *testing.T) {
	got := xidMeasurements(t, ToMeasurements(journalBatch()))
	if len(got) != 1 || got[0].EntityID() != "" || got[0].Since != "boot" || len(got[0].Errors()) != 0 {
		t.Fatalf("got %+v, want one identity-less measurement", got)
	}
}

// One unparseable line fails every Xid check, as before the split.
func TestToMeasurementsXidFailureStaysNodeWide(t *testing.T) {
	parseError := observation.NewCollectionErrorObservation(xidsignals.Code.ID(), xidsignals.Code.Unit(), "xid.journal", "parse",
		observation.JournalEntryTarget("boot", "c"), nil,
		&observation.CollectionError{Category: observation.CollectionErrorCategoryParseError, SourceError: "invalid_code", Detail: "x"})
	batch := append(journalBatch(journalXid("a", 48, "0000:89:00"), journalXid("b", 48, "0000:c1:00")), parseError)
	got := xidMeasurements(t, ToMeasurements(batch))
	if len(got) != 1 || got[0].EntityID() != "" || len(got[0].Errors()) != 1 ||
		!slices.Equal(got[0].Errors()[0].FailedSignals(), xid.MeasurementSignals()) {
		t.Fatalf("got %+v, want one errored measurement naming no GPU", got)
	}
}

func TestToMeasurementsXidFieldAndCanonicalFormsStillConflict(t *testing.T) {
	field := &observation.Observation{
		SignalId: "xid.event_count", Source: "toolkit.xid", Target: observation.NodeTarget(""), ObservedAt: timestamppb.Now(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_IntValue{IntValue: 0}}},
	}
	batch := append(journalBatch(journalXid("a", 48, "0000:89:00"), journalXid("b", 48, "0000:c1:00")), field)
	got := xidMeasurements(t, ToMeasurements(batch))
	if len(got) != 1 || len(got[0].Errors()) != 1 {
		t.Fatalf("got %+v, want one errored measurement", got)
	}
}
