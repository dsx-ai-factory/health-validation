// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestExistingGPURefEncodingUnchanged(t *testing.T) {
	tests := []struct {
		ref  *GPURef
		name string
		wire string
		json string
	}{
		{name: "empty", ref: &GPURef{}, json: `{}`},
		{name: "UUID", ref: &GPURef{Uuid: "GPU-0"}, wire: "\x0a\x05GPU-0", json: `{"uuid":"GPU-0"}`},
		{name: "source local", ref: &GPURef{SourceLocalId: "dcgm-index-1"}, wire: "\x12\x0cdcgm-index-1", json: `{"sourceLocalId":"dcgm-index-1"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire, err := proto.Marshal(test.ref)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire, []byte(test.wire)) {
				t.Fatalf("GPU wire = %x, want %x", wire, test.wire)
			}
			encoded, err := protojson.Marshal(test.ref)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err = json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(test.json), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("GPU JSON = %s, want %s", encoded, test.json)
			}
		})
	}
	if GPUTarget("GPU-0").GetGpu().GetNative() != nil || GPUSourceLocalTarget("dcgm-index-1").GetGpu().GetNative() != nil {
		t.Fatal("existing GPU constructors populated native metadata")
	}
}

func TestSharedNativeEntityTargetsRoundTrip(t *testing.T) {
	targets := map[string]*ObservationTarget{
		"GPU native only":                     {Kind: &ObservationTarget_GPU{Gpu: &GPURef{Native: &NativeEntityRef{Source: "dcgm", EntityKind: 1}}}},
		"GPU UUID":                            {Kind: &ObservationTarget_GPU{Gpu: &GPURef{Uuid: "GPU-0", Native: &NativeEntityRef{Source: "dcgm", EntityKind: 1}}}},
		"GPU local ID":                        {Kind: &ObservationTarget_GPU{Gpu: &GPURef{SourceLocalId: "dcgm-index-7", Native: &NativeEntityRef{Source: "dcgm", EntityKind: 1, EntityId: 7}}}},
		"switch native only":                  {Kind: &ObservationTarget_NVSwitch{NvSwitch: &NVSwitchRef{Native: &NativeEntityRef{Source: "dcgm", EntityKind: 3, EntityId: 17}}}},
		"switch UUID":                         {Kind: &ObservationTarget_NVSwitch{NvSwitch: &NVSwitchRef{Uuid: "switch-known-uuid", Native: &NativeEntityRef{Source: "dcgm", EntityKind: 3, EntityId: 17}}}},
		"switch UUID without native metadata": {Kind: &ObservationTarget_NVSwitch{NvSwitch: &NVSwitchRef{Uuid: "switch-known-uuid"}}},
		"zero native pair":                    {Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{Source: "dcgm"}}},
		"unknown native kind":                 {Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{Source: "dcgm", EntityKind: math.MaxUint32, EntityId: math.MaxUint32}}},
		"another source namespace":            {Kind: &ObservationTarget_GPU{Gpu: &GPURef{Native: &NativeEntityRef{Source: "other", EntityKind: math.MaxUint32, EntityId: 7}}}},
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			batch := healthReportBatch(&DCGMHealthReport{
				Entities:  []*ObservationTarget{target},
				Incidents: []*DCGMHealthIncident{{Entity: target, NativeHealth: DCGMHealthResultFail.Enum()}},
			})
			batch.Observations = append(batch.Observations, extendedObservation(target))
			if err := ValidateBatch(batch); err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := protojson.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			for format, unmarshal := range map[string]func(*ObservationBatch) error{
				"binary": func(m *ObservationBatch) error { return proto.Unmarshal(wire, m) },
				"JSON":   func(m *ObservationBatch) error { return protojson.Unmarshal(encoded, m) },
			} {
				t.Run(format, func(t *testing.T) {
					got := &ObservationBatch{}
					if err := unmarshal(got); err != nil {
						t.Fatal(err)
					}
					if !proto.Equal(batch, got) {
						t.Fatalf("shared entity changed: got %v, want %v", got, batch)
					}
					if err := ValidateBatch(got); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestNativeEntityTargetValidation(t *testing.T) {
	targets := map[string]*ObservationTarget{
		"nil GPU wrapper":          {Kind: (*ObservationTarget_GPU)(nil)},
		"empty GPU payload":        {Kind: &ObservationTarget_GPU{}},
		"nil switch wrapper":       {Kind: (*ObservationTarget_NVSwitch)(nil)},
		"empty switch payload":     {Kind: &ObservationTarget_NVSwitch{}},
		"nil native wrapper":       {Kind: (*ObservationTarget_NativeEntity)(nil)},
		"empty native payload":     {Kind: &ObservationTarget_NativeEntity{}},
		"empty kind":               {},
		"blank GPU source":         {Kind: &ObservationTarget_GPU{Gpu: &GPURef{Uuid: "GPU-0", Native: &NativeEntityRef{}}}},
		"whitespace switch source": {Kind: &ObservationTarget_NVSwitch{NvSwitch: &NVSwitchRef{Native: &NativeEntityRef{Source: " \n"}}}},
		"blank fallback source":    {Kind: &ObservationTarget_NativeEntity{NativeEntity: &NativeEntityRef{}}},
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			if err := ValidateBatch(&ObservationBatch{Observations: []*Observation{extendedObservation(target)}}); err == nil {
				t.Fatal("invalid native target accepted")
			}
			if err := ValidateBatch(healthReportBatch(&DCGMHealthReport{Entities: []*ObservationTarget{target}})); err == nil {
				t.Fatal("invalid health membership accepted")
			}
			if err := ValidateBatch(healthReportBatch(&DCGMHealthReport{Incidents: []*DCGMHealthIncident{{Entity: target}}})); err == nil {
				t.Fatal("invalid incident identity accepted")
			}
		})
	}
	for _, target := range []*ObservationTarget{NodeTarget("node"), NVLinkEndpointTarget("GPU-0", 1), GPUPairTarget("GPU-0", "GPU-1")} {
		if err := ValidateBatch(healthReportBatch(&DCGMHealthReport{Entities: []*ObservationTarget{target}})); err == nil {
			t.Fatalf("inappropriate health member accepted: %v", target)
		}
		if err := ValidateBatch(healthReportBatch(&DCGMHealthReport{Incidents: []*DCGMHealthIncident{{Entity: target}}})); err == nil {
			t.Fatalf("inappropriate incident identity accepted: %v", target)
		}
	}
}

func TestNestedGPUNativeMetadataValidation(t *testing.T) {
	for name, build := range map[string]func(*GPURef) *ObservationTarget{
		"NVLink endpoint": func(gpu *GPURef) *ObservationTarget {
			return &ObservationTarget{Kind: &ObservationTarget_NVLinkEndpoint{NvLinkEndpoint: &NVLinkEndpointRef{Gpu: gpu}}}
		},
		"GPU pair source": func(gpu *GPURef) *ObservationTarget {
			return &ObservationTarget{Kind: &ObservationTarget_GPUPair{GpuPair: &GPUPairRef{SourceGpu: gpu, PeerGpu: &GPURef{Uuid: "GPU-peer"}}}}
		},
		"GPU pair peer": func(gpu *GPURef) *ObservationTarget {
			return &ObservationTarget{Kind: &ObservationTarget_GPUPair{GpuPair: &GPUPairRef{SourceGpu: &GPURef{Uuid: "GPU-source"}, PeerGpu: gpu}}}
		},
		"GPU process": func(gpu *GPURef) *ObservationTarget {
			return &ObservationTarget{Kind: &ObservationTarget_GPUProcess{GpuProcess: &GPUProcessRef{Gpu: gpu, Process: &ProcessRef{BootId: extendedBootID, Pid: 42}}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			good := build(&GPURef{Native: &NativeEntityRef{Source: "dcgm", EntityKind: 1}})
			if err := ValidateBatch(&ObservationBatch{Observations: []*Observation{extendedObservation(good)}}); err != nil {
				t.Fatalf("native-only GPU rejected: %v", err)
			}
			bad := build(&GPURef{Uuid: "GPU-0", Native: &NativeEntityRef{Source: " \n"}})
			err := ValidateBatch(&ObservationBatch{Observations: []*Observation{extendedObservation(bad)}})
			if err == nil || !strings.Contains(err.Error(), "native entity requires a source") {
				t.Fatalf("invalid nested native metadata accepted: %v", err)
			}
		})
	}
}
