// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	extendedBootID = "01234567-89ab-cdef-0123-456789abcdef"
	extendedDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func extendedObservation(target *ObservationTarget) *Observation {
	return &Observation{
		SignalId: "test.raw.counter", Source: "test.fixture", Target: target,
		ObservedAt: timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		Outcome:    &Observation_Value{Value: &Value{Kind: &Value_UintValue{UintValue: ^uint64(0)}}},
	}
}

func TestExtendedTargetsRoundTrip(t *testing.T) {
	targets := map[string]*ObservationTarget{
		"process":         ProcessTarget(extendedBootID, 42, ^uint64(0)),
		"GPU process":     GPUProcessTarget("GPU-0", extendedBootID, 42, ^uint64(0)),
		"block device":    BlockDeviceTarget(259, 7),
		"mount":           MountTarget("mnt:[4026531841]", ^uint64(0)),
		"PCI":             PCITarget("0000:af:1f.7"),
		"FUSE connection": FUSEConnectionTarget(extendedBootID, ^uint64(0)),
		"pstore record":   PstoreRecordTarget("var/lib/systemd/pstore/123/dmesg.txt", extendedDigest),
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			batch := &ObservationBatch{CollectionId: "roundtrip", Observations: []*Observation{extendedObservation(target)}}
			if err := ValidateBatch(batch); err != nil {
				t.Fatalf("original validation: %v", err)
			}
			wire, err := proto.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			var binaryCopy ObservationBatch
			if err := proto.Unmarshal(wire, &binaryCopy); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(batch, &binaryCopy) {
				t.Fatalf("binary round trip changed identity/value: %v", &binaryCopy)
			}
			encoded, err := protojson.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			var jsonCopy ObservationBatch
			if err := protojson.Unmarshal(encoded, &jsonCopy); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(batch, &jsonCopy) {
				t.Fatalf("JSON round trip changed identity/value: %s", encoded)
			}
			if err := ValidateBatch(&jsonCopy); err != nil {
				t.Fatalf("JSON validation: %v", err)
			}
		})
	}
}

func TestUint64MaximumJSONIsExact(t *testing.T) {
	value := &Value{Kind: &Value_UintValue{UintValue: ^uint64(0)}}
	encoded, err := protojson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if got := string(fields["uintValue"]); got != `"18446744073709551615"` {
		t.Fatalf("uint64 JSON = %s", encoded)
	}
	var restored Value
	if err := protojson.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.GetUintValue() != ^uint64(0) {
		t.Fatalf("uint64 changed to %d", restored.GetUintValue())
	}
	if _, ok := restored.Kind.(*Value_UintValue); !ok {
		t.Fatalf("uint64 oneof changed: %T", restored.Kind)
	}
}

func TestSampleIntervalContract(t *testing.T) {
	start := timestamppb.New(time.Unix(100, 1))
	end := timestamppb.New(time.Unix(100, 2))
	tests := []struct {
		name       string
		start, end *timestamppb.Timestamp
		wantError  string
	}{
		{name: "snapshot"},
		{name: "positive nanosecond interval", start: start, end: end},
		{name: "start only", start: start, wantError: "incomplete sample interval"},
		{name: "end only", end: end, wantError: "incomplete sample interval"},
		{name: "reversed", start: end, end: start, wantError: "nonpositive sample interval"},
		{name: "equal", start: start, end: start, wantError: "nonpositive sample interval"},
		{name: "invalid start", start: &timestamppb.Timestamp{Nanos: -1}, end: end, wantError: "sample start"},
		{name: "invalid end", start: start, end: &timestamppb.Timestamp{Seconds: 253402300800}, wantError: "sample end"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := extendedObservation(NodeTarget(""))
			current.SampleStartedAt = test.start
			current.SampleEndedAt = test.end
			batch := &ObservationBatch{Observations: []*Observation{current}}
			err := ValidateBatch(batch)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("validation error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, binary := range []bool{true, false} {
				var encoded []byte
				if binary {
					encoded, err = proto.Marshal(batch)
				} else {
					encoded, err = protojson.Marshal(batch)
				}
				if err != nil {
					t.Fatal(err)
				}
				var restored ObservationBatch
				if binary {
					err = proto.Unmarshal(encoded, &restored)
				} else {
					err = protojson.Unmarshal(encoded, &restored)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !proto.Equal(batch, &restored) {
					t.Fatalf("interval round trip changed samples: %v", &restored)
				}
			}
		})
	}
}

func TestTypedNilTargetWrappers(t *testing.T) {
	targets := map[string]*ObservationTarget{
		"node":              {Kind: (*ObservationTarget_Node)(nil)},
		"GPU":               {Kind: (*ObservationTarget_GPU)(nil)},
		"NVLink":            {Kind: (*ObservationTarget_NVLinkEndpoint)(nil)},
		"GPU pair":          {Kind: (*ObservationTarget_GPUPair)(nil)},
		"InfiniBand device": {Kind: (*ObservationTarget_InfiniBandDevice)(nil)},
		"InfiniBand port":   {Kind: (*ObservationTarget_InfiniBandPort)(nil)},
		"kernel record":     {Kind: (*ObservationTarget_KernelRecord)(nil)},
		"process":           {Kind: (*ObservationTarget_Process)(nil)},
		"GPU process":       {Kind: (*ObservationTarget_GPUProcess)(nil)},
		"block device":      {Kind: (*ObservationTarget_BlockDevice)(nil)},
		"mount":             {Kind: (*ObservationTarget_Mount)(nil)},
		"PCI":               {Kind: (*ObservationTarget_PCI)(nil)},
		"FUSE connection":   {Kind: (*ObservationTarget_FUSEConnection)(nil)},
		"pstore record":     {Kind: (*ObservationTarget_PstoreRecord)(nil)},
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			if err := ValidateBatch(&ObservationBatch{Observations: []*Observation{extendedObservation(target)}}); err == nil {
				t.Fatal("typed nil target wrapper accepted")
			}
		})
	}
}

func TestExtendedTargetPayloadsAndIdentity(t *testing.T) {
	tests := []struct {
		target *ObservationTarget
		name   string
		valid  bool
	}{
		{name: "source-level", target: nil, valid: true},
		{name: "empty host-local node", target: NodeTarget(""), valid: true},
		{name: "process starting at tick zero", target: ProcessTarget(extendedBootID, 1, 0), valid: true},
		{name: "block zero device number", target: BlockDeviceTarget(0, 0), valid: true},
		{name: "configured mount scope", target: MountTarget("fixture-host", 1), valid: true},
		{name: "process missing payload", target: &ObservationTarget{Kind: &ObservationTarget_Process{}}, valid: false},
		{name: "process missing boot", target: ProcessTarget("", 42, 1), valid: false},
		{name: "process whitespace boot", target: ProcessTarget(" \n", 42, 1), valid: false},
		{name: "process zero PID", target: ProcessTarget(extendedBootID, 0, 1), valid: false},
		{name: "GPU process missing payload", target: &ObservationTarget{Kind: &ObservationTarget_GPUProcess{}}, valid: false},
		{name: "GPU process missing GPU", target: &ObservationTarget{Kind: &ObservationTarget_GPUProcess{GpuProcess: &GPUProcessRef{Process: &ProcessRef{BootId: extendedBootID, Pid: 42}}}}, valid: false},
		{name: "GPU process missing process", target: &ObservationTarget{Kind: &ObservationTarget_GPUProcess{GpuProcess: &GPUProcessRef{Gpu: &GPURef{Uuid: "GPU-0"}}}}, valid: false},
		{name: "GPU process missing GPU identity", target: GPUProcessTarget("", extendedBootID, 42, 1), valid: false},
		{name: "GPU process missing boot", target: GPUProcessTarget("GPU-0", "", 42, 1), valid: false},
		{name: "GPU process zero PID", target: GPUProcessTarget("GPU-0", extendedBootID, 0, 1), valid: false},
		{name: "GPU process local GPU identity", target: &ObservationTarget{Kind: &ObservationTarget_GPUProcess{GpuProcess: &GPUProcessRef{Gpu: &GPURef{SourceLocalId: "nvml-index-0"}, Process: &ProcessRef{BootId: extendedBootID, Pid: 42}}}}, valid: true},
		{name: "block device missing payload", target: &ObservationTarget{Kind: &ObservationTarget_BlockDevice{}}, valid: false},
		{name: "mount missing payload", target: &ObservationTarget{Kind: &ObservationTarget_Mount{}}, valid: false},
		{name: "mount missing namespace", target: MountTarget("", 1), valid: false},
		{name: "mount zero ID", target: MountTarget("mnt:[1]", 0), valid: false},
		{name: "PCI missing payload", target: &ObservationTarget{Kind: &ObservationTarget_PCI{}}, valid: false},
		{name: "PCI missing domain", target: PCITarget("af:00.0"), valid: false},
		{name: "PCI uppercase noncanonical", target: PCITarget("0000:AF:00.0"), valid: false},
		{name: "PCI invalid device", target: PCITarget("0000:af:20.0"), valid: false},
		{name: "PCI invalid function", target: PCITarget("0000:af:00.8"), valid: false},
		{name: "FUSE missing payload", target: &ObservationTarget{Kind: &ObservationTarget_FUSEConnection{}}, valid: false},
		{name: "FUSE missing boot", target: FUSEConnectionTarget("", 1), valid: false},
		{name: "FUSE zero ID", target: FUSEConnectionTarget(extendedBootID, 0), valid: false},
		{name: "pstore missing payload", target: &ObservationTarget{Kind: &ObservationTarget_PstoreRecord{}}, valid: false},
		{name: "pstore missing path", target: PstoreRecordTarget("", extendedDigest), valid: false},
		{name: "pstore missing hash", target: PstoreRecordTarget("sys/fs/pstore/raw", ""), valid: false},
		{name: "pstore short hash", target: PstoreRecordTarget("sys/fs/pstore/raw", "abcd"), valid: false},
		{name: "pstore nonhex hash", target: PstoreRecordTarget("sys/fs/pstore/raw", strings.Repeat("g", 64)), valid: false},
		{name: "pstore uppercase hash", target: PstoreRecordTarget("sys/fs/pstore/raw", strings.ToUpper(extendedDigest)), valid: false},
		{name: "pstore absolute provenance", target: PstoreRecordTarget("/sys/fs/pstore/raw", extendedDigest), valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBatch(&ObservationBatch{Observations: []*Observation{extendedObservation(test.target)}})
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, validation error = %v", test.valid, err)
			}
		})
	}
}
