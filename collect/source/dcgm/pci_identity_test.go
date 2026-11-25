// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
)

func TestHealthIdentityEnrichmentPreservesIndependentFields(t *testing.T) {
	unavailable := &observation.CollectionError{Source: Name, Operation: "identity", Category: observation.CollectionErrorCategoryUnavailable}
	for _, test := range []struct {
		lookupError error
		name        string
		identity    GPUIdentityEntry
	}{
		{name: "UUID and PCI address", identity: GPUIdentityEntry{UUID: "GPU-7", BusID: "0000:53:00.0"}},
		{name: "UUID unavailable", identity: GPUIdentityEntry{UUIDError: unavailable, BusID: "0000:53:00.0"}},
		{name: "PCI address unavailable", identity: GPUIdentityEntry{UUID: "GPU-7", BusIDError: unavailable}},
		{name: "both unavailable", identity: GPUIdentityEntry{UUIDError: unavailable, BusIDError: unavailable}},
		{name: "lookup failed", lookupError: errors.New("identity lookup failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := &observation.DCGMHealthReport{
				GroupResults: []*observation.DCGMHealthGroupResult{{
					EntityKind: gpuEntityKind, Systems: dcgmhealth.SystemMemory, NativeOverallHealth: observation.DCGMHealthResultFail.Enum(),
					EntityResults: []*observation.DCGMHealthEntityResult{{
						Entity: healthTestGPUTarget(7), GroupMember: proto.Bool(true),
						Incidents: []*observation.DCGMHealthIncident{{
							System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(),
							ErrorCode: 123, Message: "memory fault",
						}},
					}},
				}},
			}
			native := &healthLifecycleNative{
				health: &fakeHealthWatch{report: raw},
				cleanupNative: cleanupNative{fakeNative: fakeNative{
					uuidEntries: map[uint]GPUIdentityEntry{7: test.identity}, uuidErr: test.lookupError,
				}},
			}
			source := newSource(native, &sync.Mutex{})
			t.Cleanup(func() { _ = source.Close() })
			if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
				t.Fatal(err)
			}
			result := source.Collect(context.Background(), newTestCycle(t, test.name), CollectionRequest{Health: true})
			report, failure := result.HealthReport, result.HealthError
			entities := report.GetGroupResults()[0].GetEntityResults()
			if failure != nil || len(entities) != 1 || !entities[0].GetGroupMember() {
				t.Fatalf("optional identity lookup discarded the health result: %v, %v", report, failure)
			}
			incidents := entities[0].GetIncidents()
			if len(incidents) != 1 || incidents[0].GetErrorCode() != 123 || incidents[0].GetMessage() != "memory fault" {
				t.Fatalf("optional identity lookup discarded the incident: %v", incidents)
			}
			gpu := entities[0].GetEntity().GetGpu()
			if gpu.GetUuid() != test.identity.UUID || gpu.GetBusId() != test.identity.BusID || gpu.GetNative().GetEntityId() != 7 {
				t.Fatalf("incident GPU = %v, want UUID %q and PCI address %q", gpu, test.identity.UUID, test.identity.BusID)
			}
			if native.uuidCalls.Load() != 1 || native.enumerationCalls.Load() != 0 {
				t.Fatalf("identity enrichment repeated the lookup or enumerated GPUs: lookups=%d enumeration=%d", native.uuidCalls.Load(), native.enumerationCalls.Load())
			}
			rawGPU := raw.GetGroupResults()[0].GetEntityResults()[0].GetEntity().GetGpu()
			if rawGPU.GetUuid() != "" || rawGPU.GetBusId() != "" {
				t.Fatal("identity enrichment changed the native-owned report")
			}
		})
	}
}

func TestPCIIdentityConnectionLossRecoversOnNextCycle(t *testing.T) {
	native := &healthLifecycleNative{
		health: &fakeHealthWatch{report: &observation.DCGMHealthReport{
			GroupResults: []*observation.DCGMHealthGroupResult{{
				EntityKind: gpuEntityKind, Systems: dcgmhealth.SystemMemory, NativeOverallHealth: observation.DCGMHealthResultPass.Enum(),
				EntityResults: []*observation.DCGMHealthEntityResult{{Entity: healthTestGPUTarget(7), GroupMember: proto.Bool(true)}},
			}},
		}},
		cleanupNative: cleanupNative{fakeNative: fakeNative{uuidEntries: map[uint]GPUIdentityEntry{7: {
			UUID: "GPU-7", BusIDError: &observation.CollectionError{
				Category: observation.CollectionErrorCategoryUnavailable, SourceError: connectionNotValidStatus,
				Source: Name, Operation: "identity", Detail: "connection lost reading PCI address",
			},
		}}}},
	}
	source := newSource(native, &sync.Mutex{})
	t.Cleanup(func() { _ = source.Close() })
	if err := source.AddHealthToWatch(dcgmhealth.SystemMemory); err != nil {
		t.Fatal(err)
	}
	cycle := newTestCycle(t, "PCI-identity-connection-loss")
	result := source.Collect(context.Background(), cycle, CollectionRequest{Health: true})
	report, failure := result.HealthReport, result.HealthError
	entities := report.GetGroupResults()[0].GetEntityResults()
	if failure != nil || len(entities) != 1 || entities[0].GetEntity().GetGpu().GetUuid() != "GPU-7" || entities[0].GetEntity().GetGpu().GetBusId() != "" {
		t.Fatalf("PCI field error discarded a successful UUID or health read: %v, %v", report, failure)
	}
	if _, indicesFailure := source.newCollectionReader(cycle).gpuIndices(context.Background()); indicesFailure.GetSourceError() != connectionNotValidStatus {
		t.Fatalf("PCI field connection loss did not invalidate the session: %v", indicesFailure)
	}
	if native.initCalls.Load() != 1 || native.enumerationCalls.Load() != 0 || native.cleanupCalls.Load() != 0 {
		t.Fatal("same-cycle call entered or replaced the failed session")
	}
	native.uuidEntries[7] = GPUIdentityEntry{UUID: "GPU-7", BusID: "0000:53:00.0"}
	result = source.Collect(context.Background(), newTestCycle(t, "PCI-identity-recovered"), CollectionRequest{Health: true})
	report, failure = result.HealthReport, result.HealthError
	entities = report.GetGroupResults()[0].GetEntityResults()
	if failure != nil || len(entities) != 1 || entities[0].GetEntity().GetGpu().GetBusId() != "0000:53:00.0" ||
		native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 {
		t.Fatalf("next cycle did not replace the session and recover PCI enrichment: %v, %v", report, failure)
	}
}
