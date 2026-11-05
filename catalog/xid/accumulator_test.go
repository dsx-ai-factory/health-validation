// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/findings"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func accumulateWith(t *testing.T, acc xid.Accumulator, m xid.XidMeasurement, checks ...health.CheckDefinition) xid.XidDerivedMeasurement {
	t.Helper()
	defs := make([]*health.CheckDefinition, len(checks))
	for i := range checks {
		defs[i] = &checks[i]
	}
	out, err := acc.Accumulate([]health.Measurement{m}, defs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("Accumulate returned %d measurements, want 1", len(out))
	}
	return out[0].(xid.XidDerivedMeasurement)
}

func accumulate(t *testing.T, m xid.XidMeasurement, checks ...health.CheckDefinition) xid.XidDerivedMeasurement {
	t.Helper()
	return accumulateWith(t, xid.Accumulator{}, m, checks...)
}

// builtinChecks is every check the catalog ships.
func builtinChecks() []health.CheckDefinition { return xid.Checks() }

// siteChecks are checks of a consumer's own, which need a claim to cover a code.
func siteChecks(ids ...string) []health.CheckDefinition {
	var out []health.CheckDefinition
	for _, id := range ids {
		out = append(out, health.CheckDefinition{ID: id, Category: health.Xid})
	}
	return out
}

func nvlink5Event(subcode string, fatal bool, message string) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: "0018:01:00", XidCode: 149, Message: message,
		NVLink5: &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", IntrInfo: intrInfoForSubXidCode(subcode), Fatal: fatal, Link: 8},
	}
}

// The signal lists are what failure reporting and collection planning use, so
// they must name exactly the signals the structs carry.
func TestMeasurementSignalsMatchStructTags(t *testing.T) {
	tags := func(v any) []string {
		var out []string
		typ := reflect.TypeOf(v)
		for i := range typ.NumField() {
			if tag, ok := typ.Field(i).Tag.Lookup("json"); ok {
				out = append(out, strings.Split(tag, ",")[0])
			}
		}
		return out
	}
	for name, test := range map[string]struct {
		got  []string
		want []string
	}{
		"XidMeasurement":        {tags(xid.XidMeasurement{}), xid.MeasurementSignals()},
		"XidDerivedMeasurement": {tags(xid.XidDerivedMeasurement{}), xid.DerivedMeasurementSignals()},
	} {
		slices.Sort(test.got)
		slices.Sort(test.want)
		if !slices.Equal(test.got, test.want) {
			t.Errorf("%s signals = %v, struct tags = %v", name, test.want, test.got)
		}
	}
	if xid.SignalEvidence != "evidence" {
		t.Errorf("SignalEvidence = %q, want the evidence signal", xid.SignalEvidence)
	}
}

func TestAccumulator_DerivesCountsFromEvents(t *testing.T) {
	d := accumulate(t, xid.XidMeasurement{Since: "boot", Events: []xid.XidEvent{
		{XidCode: 13},
		{XidCode: 13},
		{XidCode: 48},
		nvlink5Event("149.4", true, "a"), nvlink5Event("149.4", false, "b"),
	}}, builtinChecks()...)

	if want := map[int64]int64{13: 2, 48: 1, 149: 2}; !reflect.DeepEqual(d.XidCounts, want) {
		t.Errorf("xid_counts = %v, want %v", d.XidCounts, want)
	}
	if want := map[int64]int64{149: 1}; !reflect.DeepEqual(d.FatalXidCounts, want) {
		t.Errorf("fatal_xid_counts = %v, want %v", d.FatalXidCounts, want)
	}
	if want := map[string]int64{"149.4": 2}; !reflect.DeepEqual(d.SubXidCodeCounts, want) {
		t.Errorf("sub_xid_code_counts = %v, want %v", d.SubXidCodeCounts, want)
	}
}

// With no events every count is an empty map, not nil, so a rule reads "none",
// and every check that covers a code still has (empty) evidence of its own.
func TestAccumulator_NoEventsGivesEmptyNotNilCounts(t *testing.T) {
	d := accumulate(t, xid.XidMeasurement{Since: "boot"}, builtinChecks()...)
	for name, counts := range map[string]map[int64]int64{
		"xid_counts": d.XidCounts, "fatal_xid_counts": d.FatalXidCounts,
		"unknown_xid_counts": d.UnknownXidCounts, "informational_xid_counts": d.InformationalXidCounts,
	} {
		if counts == nil || len(counts) != 0 {
			t.Errorf("%s = %v, want an empty non-nil map", name, counts)
		}
	}
	e, ok := d.Evidence["gpu_xid_13"]
	if !ok || len(e.Findings) != 0 || len(e.ErrorCodes) != 0 {
		t.Errorf("gpu_xid_13 evidence = %+v, %v; want present and empty", e, ok)
	}
}

// Each check gets the findings for the codes it covers, in order of first
// appearance. A subtype answers to its code and to itself, and identical lines
// within a finding are one group.
func TestAccumulator_EvidenceIsPerCheckAndGroupedByMostSpecificCode(t *testing.T) {
	acc := xid.Accumulator{Claims: map[string][]string{
		"all":   {"13", "149"},
		"only4": {"149.4"},
		"none":  {"48"},
	}}
	d := accumulateWith(t, acc, xid.XidMeasurement{Events: []xid.XidEvent{
		{XidCode: 13, Message: "x"},
		nvlink5Event("149.4", true, "l4"),
		{XidCode: 13, Message: "x"},
		nvlink5Event("149.5", true, "l5"),
		{XidCode: 13, Message: "y"},
	}}, siteChecks("all", "only4", "none")...)

	type summary struct {
		codes  []string
		groups []int64
		count  int64
	}
	summarize := func(e findings.Evidence) (out []summary) {
		for _, f := range e.Findings {
			var counts []int64
			for _, g := range f.Evidence.([]xid.EventGroup) {
				counts = append(counts, g.Count)
			}
			out = append(out, summary{codes: f.Codes, groups: counts, count: f.Count})
		}
		return out
	}

	if want := []summary{
		{codes: []string{"13"}, groups: []int64{2, 1}, count: 3},
		{codes: []string{"149", "149.4"}, groups: []int64{1}, count: 1},
		{codes: []string{"149", "149.5"}, groups: []int64{1}, count: 1},
	}; !reflect.DeepEqual(summarize(d.Evidence["all"]), want) {
		t.Errorf("all = %+v, want %+v", summarize(d.Evidence["all"]), want)
	}
	if got := d.Evidence["all"].ErrorCodes; !slices.Equal(got, []string{"13", "149"}) {
		t.Errorf("all error_codes = %v, want [13 149]", got)
	}
	if want := []summary{{codes: []string{"149", "149.4"}, groups: []int64{1}, count: 1}}; !reflect.DeepEqual(summarize(d.Evidence["only4"]), want) {
		t.Errorf("only4 = %+v, want %+v", summarize(d.Evidence["only4"]), want)
	}
	if e, ok := d.Evidence["none"]; !ok || len(e.Findings) != 0 || len(e.ErrorCodes) != 0 {
		t.Errorf("none = %+v, %v; want present and empty", e, ok)
	}
}

// An event with no line cannot be told apart from another, so it is never merged.
func TestAccumulator_DoesNotMergeEventsWithoutALine(t *testing.T) {
	d := accumulate(t, xid.XidMeasurement{Events: []xid.XidEvent{{XidCode: 13}, {XidCode: 13}}}, builtinChecks()...)
	fs := d.Evidence["gpu_xid_13"].Findings
	if len(fs) != 1 || fs[0].Count != 2 || len(fs[0].Evidence.([]xid.EventGroup)) != 2 {
		t.Errorf("gpu_xid_13 findings = %+v, want one finding of two ungrouped events", fs)
	}
}

func TestAccumulator_ClassifiesCodesNoCheckCovers(t *testing.T) {
	events := []xid.XidEvent{{XidCode: 13}, {XidCode: 172}, {XidCode: 9999}, {XidCode: 9999}}

	d := accumulate(t, xid.XidMeasurement{Events: events}, builtinChecks()...)
	if want := map[int64]int64{9999: 2}; !reflect.DeepEqual(d.UnknownXidCounts, want) {
		t.Errorf("unknown_xid_counts = %v, want %v", d.UnknownXidCounts, want)
	}
	if want := map[int64]int64{172: 1}; !reflect.DeepEqual(d.InformationalXidCounts, want) {
		t.Errorf("informational_xid_counts = %v, want %v", d.InformationalXidCounts, want)
	}

	// With no check registered nothing is covered, so 13 is unknown too.
	d = accumulate(t, xid.XidMeasurement{Events: events})
	if want := map[int64]int64{13: 1, 9999: 2}; !reflect.DeepEqual(d.UnknownXidCounts, want) {
		t.Errorf("unknown_xid_counts with no checks = %v, want %v", d.UnknownXidCounts, want)
	}

	// A consumer's check covers the codes it claims, whatever they are.
	acc := xid.Accumulator{Claims: map[string][]string{"site": {"9999", "172"}}}
	d = accumulateWith(t, acc, xid.XidMeasurement{Events: events}, append(builtinChecks(), siteChecks("site")...)...)
	if len(d.UnknownXidCounts) != 0 || len(d.InformationalXidCounts) != 0 {
		t.Errorf("claimed codes still reported: unknown %v informational %v", d.UnknownXidCounts, d.InformationalXidCounts)
	}
}

// A check covers a code only if it is registered, so one that is not covers
// nothing; a claim for an ID that is not a registered check claims nothing.
func TestAccumulator_OnlyRegisteredChecksCoverCodes(t *testing.T) {
	events := xid.XidMeasurement{Events: []xid.XidEvent{{XidCode: 54}, {XidCode: 9999}}}

	var withoutXid54 []health.CheckDefinition
	for _, cd := range builtinChecks() {
		if cd.ID != "gpu_xid_54" {
			withoutXid54 = append(withoutXid54, cd)
		}
	}
	d := accumulate(t, events, withoutXid54...)
	if want := map[int64]int64{54: 1, 9999: 1}; !reflect.DeepEqual(d.UnknownXidCounts, want) {
		t.Errorf("without gpu_xid_54: unknown_xid_counts = %v, want %v", d.UnknownXidCounts, want)
	}
	if _, ok := d.Evidence["gpu_xid_54"]; ok {
		t.Error("a check that is not registered has evidence")
	}

	d = accumulate(t, events, builtinChecks()...)
	if want := map[int64]int64{9999: 1}; !reflect.DeepEqual(d.UnknownXidCounts, want) {
		t.Errorf("with gpu_xid_54: unknown_xid_counts = %v, want %v", d.UnknownXidCounts, want)
	}

	// A claim names a check by ID; one that is not a registered check claims nothing.
	acc := xid.Accumulator{Claims: map[string][]string{"typo": {"9999"}}}
	d = accumulateWith(t, acc, events, builtinChecks()...)
	if want := map[int64]int64{9999: 1}; !reflect.DeepEqual(d.UnknownXidCounts, want) {
		t.Errorf("claim for an unregistered ID: unknown_xid_counts = %v, want %v", d.UnknownXidCounts, want)
	}
}

// A subtype claimed on its own covers the event that carries it.
func TestAccumulator_ClaimedSubtypeCoversItsEvent(t *testing.T) {
	acc := xid.Accumulator{Claims: map[string][]string{"site": {"149.4"}}}
	d := accumulateWith(t, acc, xid.XidMeasurement{Events: []xid.XidEvent{nvlink5Event("149.4", true, "l")}}, siteChecks("site")...)
	if len(d.UnknownXidCounts) != 0 {
		t.Errorf("unknown_xid_counts = %v, want the claimed subtype to cover its event", d.UnknownXidCounts)
	}
}

// An unreadable journal is reported failed rather than empty, since an empty
// count reads as no fault and the checks would pass.
func TestAccumulator_CarriesAFailedJournalForward(t *testing.T) {
	raw := xid.ErroredXid("boot", errors.New("journal unreadable"))
	d := accumulate(t, raw, builtinChecks()...)

	if len(d.Errors()) != 1 {
		t.Fatalf("errors = %v, want one", d.Errors())
	}
	got := d.Errors()[0].FailedSignals()
	slices.Sort(got)
	want := xid.DerivedMeasurementSignals()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("failed signals = %v, want every derived signal %v", got, want)
	}
	if d.XidCounts != nil {
		t.Errorf("xid_counts = %v, want nothing derived from an unread journal", d.XidCounts)
	}
}

func TestAccumulator_IgnoresOtherMeasurementsAndNoXid(t *testing.T) {
	out, err := xid.Accumulator{}.Accumulate([]health.Measurement{xid.XidDerivedMeasurement{}}, nil)
	if err != nil || len(out) != 0 {
		t.Errorf("Accumulate with no raw Xid measurement = %v, %v; want nothing", out, err)
	}
}

// A verdict shows an event group as the event's own fields plus a count.
func TestEventGroupEncodesFlat(t *testing.T) {
	f := findings.Finding{Codes: []string{"13"}, Count: 2, Evidence: []xid.EventGroup{{XidEvent: xid.XidEvent{XidCode: 13, Message: "m"}, Count: 2}}}
	got, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"evidence":[{"process_name":"","pci_address":"","message":"m","xid_code":13,"pid":0,"count":2}],"codes":["13"],"count":2}`
	if string(got) != want {
		t.Errorf("finding encodes as\n%s\nwant\n%s", got, want)
	}
}
