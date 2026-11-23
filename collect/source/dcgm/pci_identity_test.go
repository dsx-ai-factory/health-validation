// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"sync"
	"testing"

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
				Entities: []*observation.ObservationTarget{healthTestGPUTarget(7), healthTestGPUTarget(7)},
				Incidents: []*observation.DCGMHealthIncident{{
					System: dcgmhealth.SystemMemory, NativeHealth: observation.DCGMHealthResultFail.Enum(),
					ErrorCode: 123, Message: "memory fault", Entity: healthTestGPUTarget(7),
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
			if failure != nil || report == nil || len(report.GetIncidents()) != 1 || report.GetIncidents()[0].GetErrorCode() != 123 || report.GetIncidents()[0].GetMessage() != "memory fault" {
				t.Fatalf("optional identity lookup discarded the health result: %v, %v", report, failure)
			}
			for _, target := range append(report.GetEntities(), report.GetIncidents()[0].GetEntity()) {
				gpu := target.GetGpu()
				if gpu.GetUuid() != test.identity.UUID || gpu.GetBusId() != test.identity.BusID || gpu.GetNative().GetEntityId() != 7 {
					t.Fatalf("member or incident identity = %v, want UUID %q and PCI address %q", gpu, test.identity.UUID, test.identity.BusID)
				}
			}
			if native.uuidCalls.Load() != 1 || native.enumerationCalls.Load() != 0 {
				t.Fatalf("identity enrichment repeated the lookup or enumerated GPUs: lookups=%d enumeration=%d", native.uuidCalls.Load(), native.enumerationCalls.Load())
			}
			if raw.GetEntities()[0].GetGpu().GetUuid() != "" || raw.GetEntities()[0].GetGpu().GetBusId() != "" || raw.GetIncidents()[0].GetEntity().GetGpu().GetBusId() != "" {
				t.Fatal("identity enrichment changed the native-owned report")
			}
		})
	}
}

func TestPCIIdentityConnectionLossRecoversOnNextCycle(t *testing.T) {
	native := &healthLifecycleNative{
		health: &fakeHealthWatch{report: &observation.DCGMHealthReport{Entities: []*observation.ObservationTarget{healthTestGPUTarget(7)}}},
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
	if failure != nil || report == nil || report.GetEntities()[0].GetGpu().GetUuid() != "GPU-7" || report.GetEntities()[0].GetGpu().GetBusId() != "" {
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
	if failure != nil || report.GetEntities()[0].GetGpu().GetBusId() != "0000:53:00.0" || native.initCalls.Load() != 2 || native.cleanupCalls.Load() != 1 {
		t.Fatalf("next cycle did not replace the session and recover PCI enrichment: %v, %v", report, failure)
	}
}
