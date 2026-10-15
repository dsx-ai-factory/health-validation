// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectbridge_test

import (
	"testing"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"google.golang.org/protobuf/proto"
)

func TestCollectRunnerGPUProcessValues(t *testing.T) {
	const bootID = "01234567-89ab-cdef-0123-456789abcdef"
	for _, definition := range gpusignals.ProcessEntitySignals() {
		t.Run(definition.ID(), func(t *testing.T) {
			current := runnerObservation(definition, observation.GPUProcessTarget("GPU-1", bootID, 42, 500), 0)
			switch definition {
			case gpusignals.ProcessMemoryUsed:
				current.Outcome = &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_UintValue{UintValue: 1 << 63}}}
			case gpusignals.ProcessMember, gpusignals.ProcessCmdlineTruncated:
				current.Outcome = &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_BoolValue{BoolValue: true}}}
			default:
				current.Outcome = &observation.Observation_Value{Value: &observation.Value{Kind: &observation.Value_StringValue{StringValue: "process metadata"}}}
			}
			r := newCollectRunner(t, time.Second)
			registerRunner(t, r, runnerSource{id: "process", signals: collect.Signals{definition}, measure: runnerBatch(current)})
			r.Collect()
			got := r.Observe(definition.ID())
			if len(got) != 1 || !proto.Equal(current, got[0]) {
				t.Fatalf("GPU process value did not survive runner: %v", got)
			}
		})
	}
}

func TestCollectRunnerEntityParentOutcomes(t *testing.T) {
	for _, test := range []struct {
		definition collect.Signal
		current    *observation.Observation
		name       string
	}{
		{name: "idle GPU", definition: gpusignals.ProcessMember, current: observation.NewNoValueObservation(gpusignals.ProcessMember.ID(), gpusignals.ProcessMember.Unit(), "fake", observation.GPUTarget("GPU-1"), nil, &observation.NoValue{Reason: observation.NoValueReasonNoTargets})},
		{name: "unsupported NVLink", definition: gpusignals.NVLinkEndpointCRCFlitErrorCount, current: observation.NewCollectionErrorObservation(gpusignals.NVLinkEndpointCRCFlitErrorCount.ID(), gpusignals.NVLinkEndpointCRCFlitErrorCount.Unit(), "fake", "GetNvLinkState", observation.GPUTarget("GPU-1"), nil, &observation.CollectionError{Category: observation.CollectionErrorCategoryUnsupported, SourceError: "ERROR_NOT_SUPPORTED"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newCollectRunner(t, time.Second)
			registerRunner(t, r, runnerSource{id: "entity", signals: collect.Signals{test.definition}, measure: runnerBatch(test.current)})
			r.Collect()
			got := r.Observe(test.definition.ID())
			if len(got) != 1 || !proto.Equal(test.current, got[0]) {
				t.Fatalf("parent outcome changed: %v", got)
			}
		})
	}
}

func TestCollectRunnerRejectsWrongEntityScope(t *testing.T) {
	const bootID = "01234567-89ab-cdef-0123-456789abcdef"
	sourceLocal := observation.GPUProcessTarget("GPU-1", bootID, 42, 500)
	sourceLocal.GetGpuProcess().Gpu = &observation.GPURef{SourceLocalId: "1"}
	for _, test := range []struct {
		definition collect.Signal
		current    *observation.Observation
		name       string
	}{
		{name: "process value at parent", definition: gpusignals.ProcessMemoryUsed, current: runnerObservation(gpusignals.ProcessMemoryUsed, observation.GPUTarget("GPU-1"), 128)},
		{name: "NVLink value at parent", definition: gpusignals.NVLinkEndpointCRCFlitErrorCount, current: runnerObservation(gpusignals.NVLinkEndpointCRCFlitErrorCount, observation.GPUTarget("GPU-1"), 1)},
		{name: "unrelated parent no targets", definition: gpusignals.PerformanceState, current: observation.NewNoValueObservation(gpusignals.PerformanceState.ID(), gpusignals.PerformanceState.Unit(), "fake", observation.GPUTarget("GPU-1"), nil, &observation.NoValue{Reason: observation.NoValueReasonNoTargets})},
		{name: "source-local process value", definition: gpusignals.ProcessMemoryUsed, current: runnerObservation(gpusignals.ProcessMemoryUsed, sourceLocal, 128)},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newCollectRunner(t, time.Second)
			registerRunner(t, r, runnerSource{id: "invalid", signals: collect.Signals{test.definition}, measure: runnerBatch(test.current)})
			r.Collect()
			got := r.Observe(test.definition.ID())
			if len(got) != 1 || got[0].GetCollectionError() == nil || got[0].GetValue() != nil || got[0].GetNoValue() != nil {
				t.Fatalf("invalid entity scope passed: %v", got)
			}
		})
	}
}
