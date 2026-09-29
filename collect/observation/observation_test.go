// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"strings"
	"testing"
)

func TestGPURefNamesAndFallback(t *testing.T) {
	target := GPUTarget("GPU-0")
	ref := target.GetGpu()
	if ref.GetUuid() != "GPU-0" {
		t.Fatalf("GPU target = %v", target)
	}
	fallback := GPUSourceLocalTarget("dcgm-index-1").GetGpu()
	if fallback.GetUuid() != "" || fallback.GetSourceLocalId() != "dcgm-index-1" {
		t.Fatalf("GPU fallback = %v", fallback)
	}
}

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
			name: "no value",
			observation: &Observation{
				SignalId: "gpu.test",
				Outcome: &Observation_NoValue{
					NoValue: &NoValue{Reason: NoValueReasonNoTargets},
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
		{
			name: "nil no value outcome",
			observation: &Observation{
				SignalId: "gpu.test",
				Outcome:  &Observation_NoValue{},
			},
			wantError: "has an empty no-value outcome",
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

func TestValidateObservationTarget(t *testing.T) {
	tests := []struct {
		target    *ObservationTarget
		name      string
		wantError string
	}{
		{name: "source level", target: nil},
		{name: "node", target: NodeTarget("node-0")},
		{name: "GPU", target: GPUTarget("GPU-0")},
		{name: "NVLink endpoint", target: NVLinkEndpointTarget("GPU-0", 3)},
		{name: "GPU pair", target: GPUPairTarget("GPU-0", "GPU-1")},
		{name: "missing kind", target: &ObservationTarget{}, wantError: "target has no kind"},
		{
			name: "incomplete NVLink endpoint",
			target: &ObservationTarget{Kind: &ObservationTarget_NVLinkEndpoint{
				NvLinkEndpoint: &NVLinkEndpointRef{},
			}},
			wantError: "target has an incomplete NVLink endpoint payload",
		},
		{
			name: "incomplete GPU pair",
			target: &ObservationTarget{Kind: &ObservationTarget_GPUPair{
				GpuPair: &GPUPairRef{SourceGpu: &GPURef{Uuid: "GPU-0"}},
			}},
			wantError: "target has an incomplete GPU pair payload",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &Observation{
				SignalId: "gpu.test",
				Target:   test.target,
				Outcome: &Observation_Value{Value: &Value{
					Kind: &Value_IntValue{IntValue: 1},
				}},
			}
			err := validate(current)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validate() error = %v, want text %q", err, test.wantError)
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

func TestNewNoValueObservationOwnsNoValue(t *testing.T) {
	noValue := &NoValue{
		Reason:       NoValueReasonNotSupported,
		Detail:       "field is not supported",
		SourceStatus: "DCGM_FT_NOT_SUPPORTED",
	}

	first := NewNoValueObservation("gpu.test", nil, "dcgm", GPUTarget("GPU-0"), nil, noValue)
	second := NewNoValueObservation("gpu.test", nil, "dcgm", GPUTarget("GPU-0"), nil, noValue)
	firstNoValue := first.GetNoValue()
	secondNoValue := second.GetNoValue()

	if firstNoValue == noValue || secondNoValue == noValue || firstNoValue == secondNoValue {
		t.Fatal("no-value observations share mutable NoValue storage")
	}
	firstNoValue.Detail = "changed"
	if secondNoValue.GetDetail() != "field is not supported" {
		t.Fatalf("mutating first no-value changed second no-value to %q", secondNoValue.GetDetail())
	}
}
