// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
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

func TestGPUInventoryExample(t *testing.T) {
	data, err := os.ReadFile("testdata/gpu_inventory.json")
	if err != nil {
		t.Fatalf("read GPU inventory example: %v", err)
	}

	batch := &ObservationBatch{}
	if err := protojson.Unmarshal(data, batch); err != nil {
		t.Fatalf("unmarshal GPU inventory example: %v", err)
	}

	snapshots := batch.GetInventory().GetGpuSnapshots()
	if len(snapshots) != 1 {
		t.Fatalf("GPU snapshots = %d, want 1", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.GetSource() != "dcgm" {
		t.Fatalf("snapshot source = %q, want %q", snapshot.GetSource(), "dcgm")
	}
	entries := snapshot.GetInventory().GetEntries()
	if len(entries) != 2 {
		t.Fatalf("GPU entries = %d, want 2", len(entries))
	}
	if snapshot.GetCollectedAt() == nil {
		t.Fatal("GPU snapshot collection time is absent")
	}
	if entries[0].GetSourceIndex() != 0 || entries[0].GetDevice().GetId() != "GPU-123" {
		t.Fatalf("first GPU entry = %v, want source index 0 and ID GPU-123", entries[0])
	}
	device := entries[0].GetDevice()
	if device.GetPciBusId() != "0000:31:00.0" || device.GetMinorNumber() != 0 || device.MinorNumber == nil ||
		device.GetFabricCliqueId() != 0 || device.FabricCliqueId == nil ||
		device.GetComputeCapabilityMajor() != 9 || device.GetFramebufferTotalBytes() != 85899345920 ||
		device.GetDriverVersion() != "550.54.14" || device.GetCudaDriverVersion() != "12.4" {
		t.Fatalf("GPU inventory lost fleet-required fields: %v", device)
	}
	if entries[1].GetSourceIndex() != 1 || entries[1].GetCollectionError() == nil {
		t.Fatalf("second GPU entry = %v, want source index 1 and collection error", entries[1])
	}
	if err := ValidateBatch(batch); err != nil {
		t.Fatalf("ValidateBatch() error = %v", err)
	}
}

func TestValidateBatchRequiresGPUInventoryEntryOutcome(t *testing.T) {
	tests := []struct {
		entry     *GPUInventoryEntry
		name      string
		wantError string
	}{
		{
			name: "device",
			entry: &GPUInventoryEntry{SourceIndex: 0, Outcome: &GPUInventoryEntry_Device{
				Device: &GPUDevice{},
			}},
		},
		{
			name: "collection error",
			entry: &GPUInventoryEntry{SourceIndex: 1, Outcome: &GPUInventoryEntry_CollectionError{
				CollectionError: &CollectionError{Category: CollectionErrorCategoryUnavailable},
			}},
		},
		{
			name:      "device outcome without payload",
			entry:     &GPUInventoryEntry{SourceIndex: 2, Outcome: &GPUInventoryEntry_Device{}},
			wantError: "source index 2 has a device outcome with no payload",
		},
		{
			name:      "collection error outcome without payload",
			entry:     &GPUInventoryEntry{SourceIndex: 3, Outcome: &GPUInventoryEntry_CollectionError{}},
			wantError: "source index 3 has a collection-error outcome with no payload",
		},
		{name: "missing outcome", entry: &GPUInventoryEntry{SourceIndex: 4}, wantError: "source index 4 has no outcome"},
		{name: "nil entry", wantError: "entry is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBatch(&ObservationBatch{Inventory: &HardwareInventory{
				GpuSnapshots: []*GPUInventorySnapshot{{
					Source: "dcgm",
					Outcome: &GPUInventorySnapshot_Inventory{Inventory: &GPUInventory{
						Entries: []*GPUInventoryEntry{test.entry},
					}},
				}},
			}})
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateBatch() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateBatch() error = %v, want text %q", err, test.wantError)
			}
		})
	}
}

func TestValidateBatchRequiresGPUInventoryOutcome(t *testing.T) {
	tests := []struct {
		snapshot  *GPUInventorySnapshot
		name      string
		wantError string
	}{
		{
			name: "empty successful inventory",
			snapshot: &GPUInventorySnapshot{Source: "dcgm", Outcome: &GPUInventorySnapshot_Inventory{
				Inventory: &GPUInventory{},
			}},
		},
		{
			name: "collection error",
			snapshot: &GPUInventorySnapshot{Source: "dcgm", Outcome: &GPUInventorySnapshot_CollectionError{
				CollectionError: &CollectionError{Category: CollectionErrorCategoryUnavailable},
			}},
		},
		{
			name:      "inventory outcome without payload",
			snapshot:  &GPUInventorySnapshot{Source: "dcgm", Outcome: &GPUInventorySnapshot_Inventory{}},
			wantError: `source "dcgm" has an inventory outcome with no payload`,
		},
		{
			name:      "collection error outcome without payload",
			snapshot:  &GPUInventorySnapshot{Source: "dcgm", Outcome: &GPUInventorySnapshot_CollectionError{}},
			wantError: `source "dcgm" has a collection-error outcome with no payload`,
		},
		{name: "missing outcome", snapshot: &GPUInventorySnapshot{Source: "dcgm"}, wantError: `source "dcgm" has no outcome`},
		{name: "nil snapshot", wantError: "snapshot is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBatch(&ObservationBatch{Inventory: &HardwareInventory{
				GpuSnapshots: []*GPUInventorySnapshot{test.snapshot},
			}})
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateBatch() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateBatch() error = %v, want text %q", err, test.wantError)
			}
		})
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
