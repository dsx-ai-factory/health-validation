// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestFreezeWatchPolicyValidatesIntervals(t *testing.T) {
	for _, test := range []struct {
		name     string
		policy   WatchPolicy
		wantText string
	}{
		{name: "zero default", wantText: "default watch interval"},
		{name: "negative default", policy: WatchPolicy{DefaultInterval: -time.Second}, wantText: "default watch interval"},
		{name: "fractional default", policy: WatchPolicy{DefaultInterval: 1500 * time.Nanosecond}, wantText: "whole microseconds"},
		{name: "zero override", policy: WatchPolicy{DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{"signal": 0}}, wantText: `"signal"`},
		{name: "negative override", policy: WatchPolicy{DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{"signal": -time.Microsecond}}, wantText: `"signal"`},
		{name: "fractional override", policy: WatchPolicy{DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{"signal": 1500 * time.Nanosecond}}, wantText: "whole microseconds"},
		{name: "blank signal", policy: WatchPolicy{DefaultInterval: time.Second, SignalIntervals: map[string]time.Duration{" \t": time.Second}}, wantText: "canonical signal ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			frozen, err := freezeWatchPolicy(&test.policy)
			if err == nil || frozen != nil || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("freezeWatchPolicy() = %+v, %v; want error containing %q", frozen, err, test.wantText)
			}
		})
	}
	for _, interval := range []time.Duration{time.Microsecond, time.Second, 9223372036854775 * time.Microsecond} {
		if _, err := freezeWatchPolicy(&WatchPolicy{DefaultInterval: interval}); err != nil {
			t.Fatalf("valid interval %v rejected: %v", interval, err)
		}
	}
	if frozen, err := freezeWatchPolicy(nil); err != nil || frozen != nil {
		t.Fatalf("nil policy = %+v, %v", frozen, err)
	}
}

func TestFreezeWatchPolicyCopiesCallerInput(t *testing.T) {
	policy := &WatchPolicy{DefaultInterval: 30 * time.Second, SignalIntervals: map[string]time.Duration{"signal": time.Second}}
	frozen, err := freezeWatchPolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.DefaultInterval = time.Minute
	policy.SignalIntervals["signal"] = time.Minute
	policy.SignalIntervals["added"] = time.Minute
	if frozen.DefaultInterval != 30*time.Second || len(frozen.SignalIntervals) != 1 || frozen.SignalIntervals["signal"] != time.Second {
		t.Fatalf("frozen policy changed with caller input: %+v", frozen)
	}
}

func TestGroupMetricFieldsDefaultPreservesLegacyOrder(t *testing.T) {
	groups, err := groupMetricFields([]metricWatchField{
		{signalID: "first", fieldID: 8},
		{signalID: "second", fieldID: 2},
		{signalID: "alias", fieldID: 8},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].interval != 0 || !slices.Equal(groups[0].fieldIDs, []uint16{8, 2}) {
		t.Fatalf("legacy grouping = %+v, want one default watch of fields [8 2]", groups)
	}
}

func TestGroupMetricFieldsAliasesAndDeterministicGrouping(t *testing.T) {
	fields := []metricWatchField{
		{signalID: "slow", fieldID: 9},
		{signalID: "fast alias", fieldID: 9},
		{signalID: "default", fieldID: 2},
		{signalID: "slow override", fieldID: 3},
		{signalID: "fast", fieldID: 8},
		{signalID: "default alias", fieldID: 3},
	}
	policy := &WatchPolicy{DefaultInterval: 30 * time.Second, SignalIntervals: map[string]time.Duration{
		"slow": time.Minute, "fast alias": time.Second, "slow override": time.Minute, "fast": time.Second,
	}}
	want := []metricWatchGroup{
		{fieldIDs: []uint16{8, 9}, interval: time.Second},
		{fieldIDs: []uint16{2, 3}, interval: 30 * time.Second},
	}
	for iteration := 0; iteration < len(fields); iteration++ {
		groups, err := groupMetricFields(fields, policy)
		if err != nil || !reflect.DeepEqual(groups, want) {
			t.Fatalf("field grouping = %+v, %v; want %+v", groups, err, want)
		}
		fields = append(fields[1:], fields[0])
	}
}

func TestGroupMetricFieldsGroupSizeBoundaries(t *testing.T) {
	for _, test := range []struct {
		wantSizes  []int
		fieldCount int
	}{
		{fieldCount: 127, wantSizes: []int{127}},
		{fieldCount: 128, wantSizes: []int{127, 1}},
		{fieldCount: 254, wantSizes: []int{127, 127}},
	} {
		t.Run(fmt.Sprint(test.fieldCount), func(t *testing.T) {
			fields := make([]metricWatchField, test.fieldCount)
			for i := range fields {
				fields[i] = metricWatchField{signalID: fmt.Sprint(i), fieldID: uint16(test.fieldCount - i)}
			}
			groups, err := groupMetricFields(fields, &WatchPolicy{DefaultInterval: time.Second})
			if err != nil || len(groups) != len(test.wantSizes) {
				t.Fatalf("groups = %+v, %v; want sizes %v", groups, err, test.wantSizes)
			}
			var gotFields []uint16
			for i, group := range groups {
				if len(group.fieldIDs) != test.wantSizes[i] || group.interval != time.Second {
					t.Fatalf("group %d = %+v, want %d fields at one second", i, group, test.wantSizes[i])
				}
				gotFields = append(gotFields, group.fieldIDs...)
			}
			for i, fieldID := range gotFields {
				if fieldID != uint16(i+1) {
					t.Fatalf("field %d = %d, want %d", i, fieldID, i+1)
				}
			}
		})
	}
}

func TestGroupMetricFieldsIgnoresUnselectedAliasOverride(t *testing.T) {
	groups, err := groupMetricFields([]metricWatchField{
		{signalID: "selected alias", fieldID: 9},
	}, &WatchPolicy{
		DefaultInterval: 30 * time.Second,
		SignalIntervals: map[string]time.Duration{"unselected alias": time.Second},
	})
	if err != nil || len(groups) != 1 || groups[0].interval != 30*time.Second || !slices.Equal(groups[0].fieldIDs, []uint16{9}) {
		t.Fatalf("grouping with unselected override = %+v, %v; want field 9 at default interval", groups, err)
	}
}

func TestGroupMetricFieldsPreflightsSplitGroupBudget(t *testing.T) {
	for _, count := range []int{64 * 127, 64*127 + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fields := make([]metricWatchField, count)
			for i := range fields {
				fields[i] = metricWatchField{signalID: fmt.Sprint(i), fieldID: uint16(i)}
			}
			groups, err := groupMetricFields(fields, &WatchPolicy{DefaultInterval: time.Second})
			if count > 64*127 {
				if err == nil || groups != nil || !strings.Contains(err.Error(), "requires 65 field groups") {
					t.Fatalf("over-budget split configuration has %d groups, %v", len(groups), err)
				}
				return
			}
			if err != nil || len(groups) != 64 {
				t.Fatalf("within-budget split configuration has %d groups, %v", len(groups), err)
			}
			for _, group := range groups {
				if len(group.fieldIDs) != 127 {
					t.Fatalf("full-budget group has %d fields, want 127", len(group.fieldIDs))
				}
			}
		})
	}
}

func TestGroupMetricFieldsPreflightsGroupBudget(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			fields := make([]metricWatchField, count)
			policy := &WatchPolicy{DefaultInterval: time.Second, SignalIntervals: make(map[string]time.Duration, count)}
			for i := range fields {
				signalID := fmt.Sprint(i)
				fields[i] = metricWatchField{signalID: signalID, fieldID: uint16(i)}
				policy.SignalIntervals[signalID] = time.Duration(i+1) * time.Second
			}
			groups, err := groupMetricFields(fields, policy)
			if count == 65 {
				if err == nil || groups != nil || !strings.Contains(err.Error(), "requires 65 field groups") {
					t.Fatalf("over-budget configuration = %+v, %v", groups, err)
				}
			} else if err != nil || len(groups) != 64 {
				t.Fatalf("within-budget configuration has %d groups, %v", len(groups), err)
			}
		})
	}
}
