// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func scopeInput() map[string]any {
	event := xid.XidEvent{PCIAddress: "0000:89:00", XidCode: 13, Message: "NVRM: Xid (PCI:0000:89:00): 13, x"}
	return map[string]any{
		xid.SignalEvents:           []xid.XidEvent{event, event, {PCIAddress: "0000:8a:00", XidCode: 48}},
		xid.SignalXidCounts:        map[int64]int64{13: 2, 48: 1},
		xid.SignalFatalXidCounts:   map[int64]int64{48: 1},
		xid.SignalSubXidCodeCounts: map[string]int64{"149.4": 1},
		xid.SignalSince:            "boot",
	}
}

// TestScopeLeavesInputUnchanged guards the values rules evaluate against:
// a verdict's observed values share them, so scoping must copy.
func TestScopeLeavesInputUnchanged(t *testing.T) {
	observed := scopeInput()
	before, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	for _, checkID := range []string{"gpu_xid_13", "gpu_xid_149_4"} {
		xid.XidMeasurement{}.ScopeObservedValues(checkID, observed)
		after, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Errorf("scoping for %s changed its input:\n%s\nwant\n%s", checkID, after, before)
		}
	}
}

// TestScopeLeavesUnknownChecksUnscoped covers a custom Xid check: the
// measurement does not know what it judges, so it reports everything.
func TestScopeLeavesUnknownChecksUnscoped(t *testing.T) {
	observed := scopeInput()
	want, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(xid.XidMeasurement{}.ScopeObservedValues("my_xid_79_burst", observed))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("custom check observed values =\n%s\nwant\n%s", got, want)
	}
}

// TestScopeGroupsOnlyEventsWithMessages checks that identical lines are
// merged but events without a message, which cannot be told apart by it,
// each stay separate.
func TestScopeGroupsOnlyEventsWithMessages(t *testing.T) {
	line := xid.XidEvent{PCIAddress: "0000:89:00", XidCode: 13, Message: "NVRM: Xid (PCI:0000:89:00): 13, x"}
	observed := map[string]any{xid.SignalEvents: []xid.XidEvent{
		line,
		{PCIAddress: "0000:89:00", XidCode: 13},
		line,
		{PCIAddress: "0000:8a:00", XidCode: 13},
	}}
	got := xid.XidMeasurement{}.ScopeObservedValues("gpu_xid_13", observed)[xid.SignalEvents].([]xid.XidEvent)
	var counts []int64
	for _, e := range got {
		counts = append(counts, e.Count)
	}
	if want := []int64{2, 1, 1}; !slices.Equal(counts, want) {
		t.Errorf("event counts = %v, want %v", counts, want)
	}
}
