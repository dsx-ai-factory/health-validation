// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func xidValue(s collect.Signal, cursor string, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId: s.ID(), Source: "xid.journal", Target: observation.JournalEntryTarget("boot", cursor),
		ObservedAt: timestamppb.New(time.Unix(1, 0)), Unit: s.Unit(), Outcome: &observation.Observation_Value{Value: v},
	}
}

func xidInt(s collect.Signal, cursor string, v int64) *observation.Observation {
	return xidValue(s, cursor, &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}})
}

func xidString(s collect.Signal, cursor, v string) *observation.Observation {
	return xidValue(s, cursor, &observation.Value{Kind: &observation.Value_StringValue{StringValue: v}})
}

func xidBool(s collect.Signal, cursor string, v bool) *observation.Observation {
	return xidValue(s, cursor, &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: v}})
}

func xidMarker() *observation.Observation {
	o := xidString(xidsignals.ScanWindow, "", xidsignals.WindowBoot)
	o.Target = observation.NodeTarget("")
	return o
}

func xidFailure(s collect.Signal, category observation.CollectionErrorCategory, target *observation.ObservationTarget) *observation.Observation {
	return observation.NewCollectionErrorObservation(s.ID(), s.Unit(), "xid.journal", "ReadKernelBoot", target, nil,
		&observation.CollectionError{Category: category, SourceError: "unrecognized_format", Detail: "detail"})
}

func xidEvent(cursor string, code int64) []*observation.Observation {
	return []*observation.Observation{xidInt(xidsignals.Code, cursor, code), xidString(xidsignals.PCIAddress, cursor, "0000:01:00")}
}

func onlyXidError(t *testing.T, measurements []health.Measurement) error {
	t.Helper()
	m := only[xid.XidMeasurement](t, measurements)
	if len(m.Errors()) != 1 {
		t.Fatalf("want one error, got %v", m.Errors())
	}
	if got := m.Errors()[0].FailedSignals(); !reflect.DeepEqual(got, xid.MeasurementSignals()) {
		t.Fatalf("failed signals = %v, want %v", got, xid.MeasurementSignals())
	}
	return m.Errors()[0].Err()
}

// requireXid compares the stored fields and requires no errors; reflect on a
// whole measurement would also compare its errors.
func requireXid(t *testing.T, got, want xid.XidMeasurement) {
	t.Helper()
	if len(got.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", got.Errors())
	}
	gotFields := []any{got.Since, got.XidCounts, got.FatalXidCounts, got.Events, got.EventCount}
	wantFields := []any{want.Since, want.XidCounts, want.FatalXidCounts, want.Events, want.EventCount}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("measurement:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestXIDAdapterIdentity(t *testing.T) {
	if XID().Name() != "xid" || !slices.Equal(slices.Sorted(maps.Keys(XID().Signals())), slices.Sorted(slices.Values(xidsignals.All().IDs()))) {
		t.Fatalf("XID() = %q %v", XID().Name(), XID().Signals())
	}
}

func TestXIDAdapterCleanScan(t *testing.T) {
	requireXid(t, only[xid.XidMeasurement](t, XID().Build([]*observation.Observation{xidMarker()})), xid.XidMeasurement{Since: "boot"})
}

func TestXIDAdapterBuildsEventsInOrder(t *testing.T) {
	obs := []*observation.Observation{
		xidInt(xidsignals.Code, "a", 79), xidString(xidsignals.PCIAddress, "a", "0000:01:00"),
		xidInt(xidsignals.PID, "a", 1234), xidString(xidsignals.ProcessName, "a", "python3"),
	}
	obs = append(obs, xidEvent("b", 79)...)
	obs = append(obs, xidEvent("c", 149)...)
	obs = append(obs,
		xidString(xidsignals.NVLink5SubComponent, "c", "NETIR_LINK_EVT"), xidBool(xidsignals.NVLink5Fatal, "c", true),
		xidInt(xidsignals.NVLink5Link, "c", 8), xidString(xidsignals.NVLink5IntrInfo, "c", "0x004505c6"),
		xidString(xidsignals.NVLink5ErrorStatus, "c", "0x00000000"), xidString(xidsignals.NVLink5DebugData, "c", "0x00000001 0x00000002"),
	)
	obs = append(obs, xidEvent("d", 145)...)
	obs = append(obs, xidString(xidsignals.NVLink5SubComponent, "d", "RLW_REMAP"), xidBool(xidsignals.NVLink5Fatal, "d", false), xidInt(xidsignals.NVLink5Link, "d", 2))
	obs = append(obs, xidEvent("e", 154)...)
	obs = append(obs,
		xidString(xidsignals.RecoveryActionFromCode, "e", "0x0"), xidString(xidsignals.RecoveryActionFromName, "e", "None"),
		xidString(xidsignals.RecoveryActionToCode, "e", "0x1"), xidString(xidsignals.RecoveryActionToName, "e", "GPU Reset Required"),
		xidMarker(),
	)
	got := only[xid.XidMeasurement](t, XID().Build(obs))
	want := xid.XidMeasurement{
		Since:          "boot",
		XidCounts:      map[int64]int64{79: 2, 149: 1, 145: 1, 154: 1},
		FatalXidCounts: map[int64]int64{149: 1},
		EventCount:     5,
		Events: []xid.XidEvent{
			{PCIAddress: "0000:01:00", XidCode: 79, Pid: 1234, ProcessName: "python3"},
			{PCIAddress: "0000:01:00", XidCode: 79},
			{PCIAddress: "0000:01:00", XidCode: 149, NVLink5: &xid.NVLink5Detail{
				SubComponent: "NETIR_LINK_EVT", Fatal: true, Link: 8, IntrInfo: "0x004505c6", ErrorStatus: "0x00000000",
				DebugData: []string{"0x00000001", "0x00000002"},
			}},
			{PCIAddress: "0000:01:00", XidCode: 145, NVLink5: &xid.NVLink5Detail{SubComponent: "RLW_REMAP", Link: 2}},
			{PCIAddress: "0000:01:00", XidCode: 154},
		},
	}
	requireXid(t, got, want)
}

func TestXIDAdapterFailsClosed(t *testing.T) {
	allFailed := func(category observation.CollectionErrorCategory) []*observation.Observation {
		var out []*observation.Observation
		for _, s := range xidsignals.All() {
			out = append(out, xidFailure(s, category, nil))
		}
		return out
	}
	wrongKind := xidString(xidsignals.Code, "a", "79")
	tests := []struct {
		name     string
		wantText string
		obs      []*observation.Observation
	}{
		{name: "no observations", wantText: "no xid observations were collected"},
		{name: "events without marker", wantText: "xid scan marker missing", obs: xidEvent("a", 79)},
		{
			name:     "parse error",
			wantText: "unrecognized_format",
			obs:      []*observation.Observation{xidFailure(xidsignals.Code, observation.CollectionErrorCategoryParseError, observation.JournalEntryTarget("boot", "a")), xidMarker()},
		},
		{
			name:     "no value",
			wantText: "xid.journal returned no value (NO_VALUE_REASON_NOT_SUPPORTED)",
			obs: []*observation.Observation{
				observation.NewNoValueObservation(xidsignals.Code.ID(), xidsignals.Code.Unit(), "xid.journal", observation.JournalEntryTarget("boot", "a"), nil, &observation.NoValue{Reason: observation.NoValueReasonNotSupported}),
				xidMarker(),
			},
		},
		{name: "missing code", wantText: "xid event 1: missing xid.event.code", obs: []*observation.Observation{xidString(xidsignals.PCIAddress, "a", "0000:01:00"), xidMarker()}},
		{name: "missing pci", wantText: "xid event 1: missing xid.event.pci_address", obs: []*observation.Observation{xidInt(xidsignals.Code, "a", 79), xidMarker()}},
		{
			name:     "incomplete NVLink5 fields",
			wantText: "xid event 1: incomplete NVLink5 fields",
			obs: append(xidEvent("a", 145),
				xidString(xidsignals.NVLink5SubComponent, "a", "RLW_REMAP"), xidInt(xidsignals.NVLink5Link, "a", 2), xidMarker()),
		},
		{name: "wrong kind", wantText: "xid event 1: signal xid.event.code has the wrong value kind", obs: []*observation.Observation{wrongKind, xidString(xidsignals.PCIAddress, "a", "0000:01:00"), xidMarker()}},
		{name: "source-scoped failure", wantText: "SOURCE_ERROR", obs: allFailed(observation.CollectionErrorCategorySourceError)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := onlyXidError(t, XID().Build(test.obs))
			if !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("error %q, want %q", err.Error(), test.wantText)
			}
		})
	}
}

func xidCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat := catalog.NewEmpty(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	for _, check := range xid.Checks() {
		if err := cat.Register(check); err != nil {
			t.Fatal(err)
		}
	}
	return cat
}

func TestXIDAdapterTimeoutKeepsRetry(t *testing.T) {
	var obs []*observation.Observation
	for _, s := range xidsignals.All() {
		obs = append(obs, xidFailure(s, observation.CollectionErrorCategoryTimeout, nil))
	}
	measurements := XID().Build(obs)
	if err := onlyXidError(t, measurements); !errors.Is(err, health.ErrCollectionTimeout) {
		t.Fatalf("timeout lost: %v", err)
	}
	verdict, err := xidCatalog(t).EvaluateAll(measurements...)
	if err != nil {
		t.Fatal(err)
	}
	if skipped := architectureSkips(verdict); len(verdict.CheckResults)+skipped != len(xid.Checks()) {
		t.Fatalf("evaluated %d checks and %d architecture skips, want %d", len(verdict.CheckResults), skipped, len(xid.Checks()))
	}
	for _, result := range verdict.CheckResults {
		if result.Severity != health.Critical || !result.RecommendedAction.Equal(health.Drain(health.RemediationActionRetryCollection)) {
			t.Fatalf("%s = %v %v, want CRITICAL retry_collection", result.CheckID, result.Severity, result.RecommendedAction)
		}
	}
}

func TestXIDAdapterMixedTimeoutAndParseError(t *testing.T) {
	obs := []*observation.Observation{
		xidFailure(xidsignals.PID, observation.CollectionErrorCategoryTimeout, nil),
		xidFailure(xidsignals.Code, observation.CollectionErrorCategoryParseError, observation.JournalEntryTarget("boot", "a")),
	}
	err := onlyXidError(t, XID().Build(obs))
	if errors.Is(err, health.ErrCollectionTimeout) || !strings.Contains(err.Error(), "PARSE_ERROR") {
		t.Fatalf("error = %v, want the parse error", err)
	}
}

func TestXIDAdapterFailureCoversDerivedSignals(t *testing.T) {
	m := only[xid.XidMeasurement](t, XID().Build(nil))
	failed := m.Errors()[0].FailedSignals()
	for key := range (xid.XidMeasurement{}).DerivedSignals() {
		if !slices.Contains(failed, key) {
			t.Fatalf("derived signal %q not failed: %v", key, failed)
		}
	}
}

func TestXIDAdapterMeasurementContracts(t *testing.T) {
	m := XID().Build([]*observation.Observation{xidMarker()})
	if err := catalog.CheckSignalUniqueness(m...); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CheckDerivedSignals(m...); err != nil {
		t.Fatal(err)
	}
}

// TestXIDAdapterAgreesWithToMeasurements guards the single owner: the adapter
// and observations.ToMeasurements convert one xid.journal batch alike.
func TestXIDAdapterAgreesWithToMeasurements(t *testing.T) {
	batch := append(xidEvent("a", 79), xidMarker())
	direct := only[xid.XidMeasurement](t, observations.ToMeasurements(batch))
	requireXid(t, only[xid.XidMeasurement](t, XID().Build(batch)), direct)
	if direct.XidCounts[79] != 1 {
		t.Fatalf("ToMeasurements counts = %v, want Xid 79", direct.XidCounts)
	}
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
