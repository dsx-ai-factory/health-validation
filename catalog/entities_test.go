// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
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
		gpu.ErroredGPU("GPU-3", errors.New("GPU is lost"), gpu.MeasurementSignals()...),
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

func xid48On(gpuID, pci string) xid.XidMeasurement {
	return xid.XidMeasurement{
		GPUID:      gpuID,
		PCIAddress: pci,
		Since:      "boot",
		XidCounts:  map[int64]int64{48: 1},
		Events:     []xid.XidEvent{{PCIAddress: pci, XidCode: 48}},
		EventCount: 1,
	}
}

// An Xid names the GPU that logged it: the UUID when one resolved, the PCI
// address otherwise. PASS results show the GPU in observed_values only.
func TestEvaluateAll_XidResultsNameTheirGPU(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	nv, err := cat.EvaluateAll(xid48On("GPU-a", "0000:89:00"), xid48On("", "0000:c1:00"))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}

	var named [][]string
	for _, v := range nv.CheckResults {
		if v.Category != health.Xid {
			continue
		}
		if v.CheckID != "gpu_xid_48" {
			if v.Severity != health.Pass || len(v.AffectedEntities) != 0 {
				t.Errorf("%s = %v %v, want PASS naming nothing", v.CheckID, v.Severity, v.AffectedEntities)
			}
			if _, ok := v.ObservedValues[xid.SignalGPUID]; !ok {
				t.Errorf("%s observed_values = %v, want gpu_id", v.CheckID, v.ObservedValues)
			}
			continue
		}
		if v.Severity != health.Critical {
			t.Errorf("gpu_xid_48 = %v, want CRITICAL", v.Severity)
		}
		if keys := slices.Sorted(maps.Keys(v.ObservedValues)); !slices.Equal(keys, []string{"gpu_id", "pci_address", "xid_counts"}) {
			t.Errorf("gpu_xid_48 observed_values keys = %v", keys)
		}
		named = append(named, v.AffectedEntities)
	}
	if want := [][]string{{"GPU-a"}, {"0000:c1:00"}}; !slices.EqualFunc(named, want, slices.Equal) {
		t.Errorf("gpu_xid_48 entities = %v, want %v", named, want)
	}
	for _, c := range nv.AffectedComponents {
		if c.Category == health.Xid && !slices.Equal(slices.Sorted(slices.Values(c.Entities)), []string{"0000:c1:00", "GPU-a"}) {
			t.Errorf("xid affected component entities = %v", c.Entities)
		}
	}
}

// A clean boot or failed scan has no GPU to name.
func TestEvaluateAll_XidWithoutIdentityNamesNoEntity(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	nv, err := cat.EvaluateAll(xid.XidMeasurement{Since: "boot", XidCounts: map[int64]int64{79: 1}, EventCount: 1})
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if got := entitiesFor(t, nv, "gpu_xid_79"); len(got) != 0 {
		t.Errorf("AffectedEntities = %v, want none", got)
	}
}
