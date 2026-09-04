// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"strings"
	"testing"
)

func TestValidateObservationRequiresOutcome(t *testing.T) {
	tests := []struct {
		observation *Observation
		name        string
		wantError   string
	}{
		{
			name: "value",
			observation: &Observation{
				SignalId: "gpu.test",
				Outcome: &Observation_Value{Value: &Value{
					Kind: &Value_IntValue{IntValue: 1},
				}},
			},
		},
		{
			name: "collection error",
			observation: &Observation{
				SignalId: "gpu.test",
				Outcome: &Observation_CollectionError{
					CollectionError: &CollectionError{Category: CollectionErrorCategoryUnavailable},
				},
			},
		},
		{
			name: "nil value outcome",
			observation: &Observation{
				SignalId: "gpu.test",
				Outcome:  &Observation_Value{},
			},
			wantError: "has an empty value outcome",
		},
		{
			name: "nil collection error outcome",
			observation: &Observation{
				SignalId: "gpu.test",
				Outcome:  &Observation_CollectionError{},
			},
			wantError: "has an empty collection-error outcome",
		},
		{name: "missing outcome", observation: &Observation{SignalId: "gpu.test"}, wantError: "has no outcome"},
		{name: "nil observation", wantError: "observation is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validate(test.observation)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateObservation() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateObservation() error = %v, want text %q", err, test.wantError)
			}
		})
	}
}

func TestValidateBatchReportsInvalidObservation(t *testing.T) {
	err := ValidateBatch(&ObservationBatch{Observations: []*Observation{
		{SignalId: "gpu.valid", Outcome: &Observation_Value{Value: &Value{}}},
		{SignalId: "gpu.invalid"},
	}})
	if err == nil || !strings.Contains(err.Error(), `observation 1: observation "gpu.invalid" has no outcome`) {
		t.Fatalf("ValidateBatch() error = %v", err)
	}
}

func TestNewCollectionErrorObservationOwnsFailure(t *testing.T) {
	unit := "1"
	failure := &CollectionError{
		Category: CollectionErrorCategoryUnavailable,
		Detail:   "device unavailable",
	}

	first := NewCollectionErrorObservation("gpu.test", &unit, "dcgm", "metrics", nil, nil, failure)
	second := NewCollectionErrorObservation("gpu.test", &unit, "dcgm", "metrics", nil, nil, failure)
	firstFailure := first.GetCollectionError()
	secondFailure := second.GetCollectionError()

	if firstFailure == failure || secondFailure == failure || firstFailure == secondFailure {
		t.Fatal("error observations share mutable CollectionError storage")
	}
	if failure.GetSource() != "" || failure.GetOperation() != "" {
		t.Fatalf("input failure was mutated: %+v", failure)
	}
	if firstFailure.GetSource() != "dcgm" || firstFailure.GetOperation() != "metrics" {
		t.Fatalf("defaults = (%q, %q), want (%q, %q)",
			firstFailure.GetSource(), firstFailure.GetOperation(), "dcgm", "metrics")
	}

	firstFailure.Detail = "changed"
	if secondFailure.GetDetail() != "device unavailable" {
		t.Fatalf("mutating first error changed second error to %q", secondFailure.GetDetail())
	}
}

func TestNewCollectionErrorObservationPreservesSourceMetadata(t *testing.T) {
	failure := &CollectionError{Source: "native", Operation: "read"}
	current := NewCollectionErrorObservation("gpu.test", nil, "dcgm", "metrics", nil, nil, failure)

	got := current.GetCollectionError()
	if got.GetSource() != "native" || got.GetOperation() != "read" {
		t.Fatalf("metadata = (%q, %q), want (%q, %q)",
			got.GetSource(), got.GetOperation(), "native", "read")
	}
}
