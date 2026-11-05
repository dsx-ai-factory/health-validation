// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestScopeMatchesAnyCodeAFindingCarries(t *testing.T) {
	fs := []Finding{
		{Codes: []string{"13"}, Count: 2},
		{Codes: []string{"149", "149.4"}, Count: 1},
		{Codes: []string{"149", "149.5"}, Count: 3},
	}
	for _, test := range []struct {
		name       string
		declares   []string
		wantCounts []int64
		wantCodes  []string
	}{
		{"one code", []string{"13"}, []int64{2}, []string{"13"}},
		{"parent code takes every subtype", []string{"149"}, []int64{1, 3}, []string{"149"}},
		{"sub-code takes only its own", []string{"149.4"}, []int64{1}, []string{"149.4"}},
		{"several", []string{"149.5", "13"}, []int64{2, 3}, []string{"13", "149.5"}},
		{"none present", []string{"48"}, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := Scope(fs, test.declares)
			var counts []int64
			for _, f := range e.Findings {
				counts = append(counts, f.Count)
			}
			if !slices.Equal(counts, test.wantCounts) || !slices.Equal(e.ErrorCodes, test.wantCodes) {
				t.Errorf("Scope = counts %v codes %v, want %v %v", counts, e.ErrorCodes, test.wantCounts, test.wantCodes)
			}
		})
	}
}

func TestFindingIsClaimedWhenAnyOfItsCodesIs(t *testing.T) {
	claimed := map[string]bool{"13": true, "149.4": true}
	if !(Finding{Codes: []string{"149", "149.4"}}).IsClaimed(claimed) {
		t.Error("a finding is claimed when any of its codes is")
	}
	if (Finding{Codes: []string{"172"}}).IsClaimed(claimed) {
		t.Error("an unclaimed code reported as claimed")
	}
}

func TestObservedIsLookedUpByCheckIDAndNeverNil(t *testing.T) {
	signals := map[string]any{Signal: map[string]Evidence{
		"has":   {ErrorCodes: []string{"13"}, Findings: []Finding{{Codes: []string{"13"}, Count: 1}}},
		"empty": {},
	}}

	got, ok := Observed(&health.CheckDefinition{ID: "has"}, signals)
	if !ok || !slices.Equal(got[ErrorCodesKey].([]string), []string{"13"}) {
		t.Errorf("scoped check: Observed = %v, %v", got, ok)
	}
	got, ok = Observed(&health.CheckDefinition{ID: "empty"}, signals)
	if !ok || got == nil || len(got) != 0 {
		t.Errorf("scoped check with nothing: Observed = %v, %v; want an empty non-nil map", got, ok)
	}
	if _, ok := Observed(&health.CheckDefinition{ID: "other"}, signals); ok {
		t.Error("a check the measurement did not scope is reported as scoped")
	}
	if _, ok := Observed(&health.CheckDefinition{ID: "has"}, map[string]any{}); ok {
		t.Error("a measurement with no evidence scopes nothing")
	}
}
