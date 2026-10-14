// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// gpuPstateCheck fails any GPU not in P0.
func gpuPstateCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "gpu_pstate_probe",
		Category:          health.GPU,
		RequiredSignals:   []string{gpu.SignalGPUID, gpu.SignalGPUPstate},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Thresholds:        map[string]health.ThresholdSpec{},
		Rules: []health.Rule{{
			Expression: "gpu_pstate != 0",
			Severity:   health.Critical,
			Message:    "wrong pstate",
		}},
	}
}

func entitiesFor(t *testing.T, nv health.NodeVerdict, checkID string) []string {
	t.Helper()
	var got []string
	for _, v := range nv.CheckResults {
		if v.CheckID == checkID {
			got = append(got, v.AffectedEntities...)
		}
	}
	return got
}

// A per-GPU check that fails on one GPU among healthy ones must name that
// GPU. Without it a consumer sees a failing check and no way to tell which
// device it was: the check ID and signals are identical across entities.
func TestEvaluateAll_FailingCheckNamesItsEntity(t *testing.T) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(gpuPstateCheck())

	nv, err := cat.EvaluateAll(
		gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0},
		gpu.GPUMeasurement{GPUID: "GPU-1", GPUPstate: 8},
		gpu.GPUMeasurement{GPUID: "GPU-2", GPUPstate: 0},
	)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}

	if got, want := entitiesFor(t, nv, "gpu_pstate_probe"), []string{"GPU-1"}; !slices.Equal(got, want) {
		t.Errorf("AffectedEntities = %v, want %v", got, want)
	}

	// The node-level rollup is built from the same field, so it names the
	// GPU too.
	if len(nv.AffectedComponents) != 1 || !slices.Equal(nv.AffectedComponents[0].Entities, []string{"GPU-1"}) {
		t.Errorf("AffectedComponents = %+v, want one naming GPU-1", nv.AffectedComponents)
	}
}

// A passing verdict names nothing: AffectedEntities is the set to act on,
// and listing every healthy GPU in it would be noise.
func TestEvaluateAll_PassingCheckNamesNoEntity(t *testing.T) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(gpuPstateCheck())

	nv, err := cat.EvaluateAll(gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 0})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if got := entitiesFor(t, nv, "gpu_pstate_probe"); len(got) != 0 {
		t.Errorf("AffectedEntities = %v, want none for a passing check", got)
	}
}

// An unobservable GPU is an affected entity: the verdict is Critical and the
// operator needs to know which device went dark.
func TestEvaluateAll_ErroredMeasurementNamesItsEntity(t *testing.T) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(gpuPstateCheck())

	nv, err := cat.EvaluateAll(
		gpu.ErroredGPU("GPU-3", errors.New("GPU is lost"), gpu.MeasurementSignals...),
	)
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if got, want := entitiesFor(t, nv, "gpu_pstate_probe"), []string{"GPU-3"}; !slices.Equal(got, want) {
		t.Errorf("AffectedEntities = %v, want %v", got, want)
	}
}

// Host-wide measurements identify no entity, so a failing host-wide check
// records none rather than a blank one.
func TestEvaluateAll_HostWideCheckNamesNoEntity(t *testing.T) {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(health.CheckDefinition{
		ID:                "cpu_probe",
		Category:          health.CPU,
		RequiredSignals:   []string{cpu.SignalOnlineCPUCount},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Thresholds:        map[string]health.ThresholdSpec{},
		Rules: []health.Rule{{
			Expression: "online_cpu_count < 2",
			Severity:   health.Critical,
			Message:    "cpus offline",
		}},
	})

	nv, err := cat.EvaluateAll(cpu.CPUMeasurement{PossibleCPUCount: 4, OnlineCPUCount: 1, NUMANodeCount: 1})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Fatalf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
	if got := entitiesFor(t, nv, "cpu_probe"); len(got) != 0 {
		t.Errorf("AffectedEntities = %v, want none for a host-wide check", got)
	}
}
