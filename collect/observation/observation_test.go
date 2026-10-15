// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
		{name: "InfiniBand device", target: InfiniBandDeviceTarget("mlx5_0")},
		{name: "InfiniBand port", target: InfiniBandPortTarget("mlx5_0", 1)},
		{name: "kernel record", target: KernelRecordTarget("boot", 42)},
		{name: "empty InfiniBand device", target: &ObservationTarget{Kind: &ObservationTarget_InfiniBandDevice{}}, wantError: "target has an empty InfiniBand device payload"},
		{name: "nil InfiniBand device", target: &ObservationTarget{Kind: (*ObservationTarget_InfiniBandDevice)(nil)}, wantError: "target has an empty InfiniBand device payload"},
		{name: "empty InfiniBand port", target: &ObservationTarget{Kind: &ObservationTarget_InfiniBandPort{}}, wantError: "target has an empty InfiniBand port payload"},
		{name: "nil InfiniBand port", target: &ObservationTarget{Kind: (*ObservationTarget_InfiniBandPort)(nil)}, wantError: "target has an empty InfiniBand port payload"},
		{name: "missing InfiniBand port device", target: &ObservationTarget{Kind: &ObservationTarget_InfiniBandPort{InfiniBandPort: &InfiniBandPortRef{Port: 1}}}, wantError: "target has an incomplete InfiniBand port payload"},
		{name: "empty kernel record", target: &ObservationTarget{Kind: &ObservationTarget_KernelRecord{}}, wantError: "target has an empty kernel record payload"},
		{name: "nil kernel record", target: &ObservationTarget{Kind: (*ObservationTarget_KernelRecord)(nil)}, wantError: "target has an empty kernel record payload"},
		{name: "journal entry", target: JournalEntryTarget("0123abcd", "s=1;i=2")},
		{name: "empty journal entry", target: &ObservationTarget{Kind: &ObservationTarget_JournalEntry{}}, wantError: "target has an empty journal entry payload"},
		{name: "nil journal entry", target: &ObservationTarget{Kind: (*ObservationTarget_JournalEntry)(nil)}, wantError: "target has an empty journal entry payload"},
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

func TestJournalEntryTargetRoundTrip(t *testing.T) {
	batch := &ObservationBatch{CollectionId: "cycle-1", Observations: []*Observation{{
		SignalId: "xid.event.code",
		Source:   "xid.journal",
		Target:   JournalEntryTarget("0123abcd", "s=1;i=2"),
		Outcome:  &Observation_Value{Value: &Value{Kind: &Value_IntValue{IntValue: 79}}},
	}}}
	jsonData, err := protojson.Marshal(batch)
	if err != nil {
		t.Fatalf("protojson.Marshal() error = %v", err)
	}
	if !strings.Contains(string(jsonData), `"journalEntry"`) {
		t.Fatalf("protojson output %s lacks journalEntry", jsonData)
	}
	binaryData, err := proto.Marshal(batch)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	for name, unmarshal := range map[string]func(*ObservationBatch) error{
		"protojson": func(m *ObservationBatch) error { return protojson.Unmarshal(jsonData, m) },
		"proto":     func(m *ObservationBatch) error { return proto.Unmarshal(binaryData, m) },
	} {
		t.Run(name, func(t *testing.T) {
			got := &ObservationBatch{}
			if err := unmarshal(got); err != nil {
				t.Fatalf("unmarshal error = %v", err)
			}
			if err := ValidateBatch(got); err != nil {
				t.Fatalf("ValidateBatch() error = %v", err)
			}
			entry := got.GetObservations()[0].GetTarget().GetJournalEntry()
			if entry.GetBootId() != "0123abcd" || entry.GetCursor() != "s=1;i=2" {
				t.Fatalf("journal entry = %v", entry)
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
	if unmarshalErr := protojson.Unmarshal(data, batch); unmarshalErr != nil {
		t.Fatalf("unmarshal GPU inventory example: %v", unmarshalErr)
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
	if len(entries) != 3 {
		t.Fatalf("GPU entries = %d, want 3", len(entries))
	}
	if snapshot.GetCollectedAt() == nil {
		t.Fatal("GPU snapshot collection time is absent")
	}
	if entries[0].GetSourceIndex() != 0 || entries[0].GetGpu().GetUuid() != "GPU-123" {
		t.Fatalf("first GPU entry = %v, want source index 0 and UUID GPU-123", entries[0])
	}
	gpu := entries[0].GetGpu()
	if gpu.GetBusId() != "0000:31:00.0" || gpu.GetMinorNumber() != 0 || gpu.MinorNumber == nil ||
		gpu.GetFabricCliqueId() != 0 || gpu.FabricCliqueId == nil ||
		gpu.GetComputeCapabilityMajor() != 9 || gpu.GetFramebufferTotalBytes() != 85899345920 ||
		gpu.GetDriverVersion() != "550.54.14" || gpu.GetCudaDriverVersion() != "12.4" {
		t.Fatalf("GPU inventory lost fleet-required fields: %v", gpu)
	}
	if entries[1].GetSourceIndex() != 1 || entries[1].GetCollectionError() == nil {
		t.Fatalf("second GPU entry = %v, want source index 1 and collection error", entries[1])
	}
	if entries[2].GetGpu().GetUuid() != "GPU-789" || entries[2].GetFieldErrors()["serial"].GetSourceError() != "DCGM_FT_BLANK" {
		t.Fatalf("partial inventory lost: %+v", entries[2])
	}
	encoded, err := protojson.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip := &ObservationBatch{}
	if err := protojson.Unmarshal(encoded, roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBatch(roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBatch(batch); err != nil {
		t.Fatalf("ValidateBatch() error = %v", err)
	}
}

func TestValidateGPUInventoryEntry(t *testing.T) {
	gpuUUID := "GPU-0"
	failure := &CollectionError{Category: CollectionErrorCategoryUnavailable}
	tests := []struct {
		entry     *GPUInventoryEntry
		name      string
		wantError bool
	}{
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{Uuid: &gpuUUID}}, name: "GPU"},
		{entry: &GPUInventoryEntry{CollectionError: failure}, name: "read failure"},
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{Uuid: &gpuUUID}, CollectionError: failure}, name: "identity survives read failure"},
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{Uuid: &gpuUUID}, FieldErrors: map[string]*CollectionError{"serial": failure}}, name: "partial attributes"},
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{}, FieldErrors: map[string]*CollectionError{"future_field": failure}}, name: "forward compatible error"},
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{Uuid: &gpuUUID}, FieldErrors: map[string]*CollectionError{"uuid": failure}}, name: "value and error conflict", wantError: true},
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{}, FieldErrors: map[string]*CollectionError{"serial": nil}}, name: "nil field error", wantError: true},
		{entry: &GPUInventoryEntry{Gpu: &GPUDevice{}, FieldErrors: map[string]*CollectionError{"": failure}}, name: "empty field name", wantError: true},
		{entry: &GPUInventoryEntry{}, name: "missing result", wantError: true},
		{name: "nil entry", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateGPUInventoryEntry(test.entry); (err != nil) != test.wantError {
				t.Fatalf("validation = %v, want error %v", err, test.wantError)
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
		Detail:   "gpu unavailable",
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
	if secondFailure.GetDetail() != "gpu unavailable" {
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
