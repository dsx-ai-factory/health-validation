// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// runAmbiguous runs a and asserts it stopped without a verdict, naming the
// check and entity on stderr.
func runAmbiguous(t *testing.T, a *App, wantInStderr ...string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitError {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitError, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no verdict", stdout.String())
	}
	for _, want := range append([]string{"ambiguous measurements"}, wantInStderr...) {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

// Two measurements of one GPU are a wiring fault, not two findings: the CLI
// reports neither and exits 2.
func TestRunRejectsDuplicateGPUMeasurements(t *testing.T) {
	a := cadenceRunApp().WithNodeID(testNodeID).WithGPUBackend(toolkit.MockGPU(
		gpu.GPUMeasurement{GPUID: "GPU-A"},
		gpu.GPUMeasurement{GPUID: "GPU-B"},
		gpu.GPUMeasurement{GPUID: "GPU-A", GPUPstate: 8},
	))
	runAmbiguous(t, a, `check "gpu_`, `entity "GPU-A"`, "gpu.GPUMeasurement, gpu.GPUMeasurement")
}

// shadowCPUAdapter is a caller adapter that avoids the reserved "cpu" name but
// still emits the CPU category, duplicating the CPU sensor's measurement.
type shadowCPUAdapter struct{}

func (shadowCPUAdapter) Name() string              { return "shadow-cpu" }
func (shadowCPUAdapter) Category() health.Category { return health.CPU }
func (shadowCPUAdapter) Signals() map[string][]string {
	return map[string][]string{customCountSignal: {cpu.SignalOnlineCPUCount}}
}

func (shadowCPUAdapter) Build([]*observation.Observation) []health.Measurement {
	return []health.Measurement{cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34}}
}

func (shadowCPUAdapter) Errored(err error) []health.Measurement {
	return []health.Measurement{cpu.ErroredCPU(err)}
}

// Name reservation only catches an adapter reusing a built-in name. The
// check/entity guard catches one that emits a built-in category under
// another name.
func TestRunRejectsCallerAdapterDuplicatingBuiltinCategory(t *testing.T) {
	a := cadenceRunApp().WithNodeID(testNodeID).
		WithCollect(&collectAppSensor{}).
		WithCollectAdapters(shadowCPUAdapter{})
	runAmbiguous(t, a, `check "cpu_count", node`)
}

// NVML's slot 0 and DCGM's slot 0 failing are two unresolved GPUs. The run
// still produces a verdict, failing each.
func TestRunKeepsSameSlotFromTwoSourcesDistinct(t *testing.T) {
	slot := func(source string) gpu.GPUMeasurement {
		m := gpu.ErroredGPU(source+"-index-0", errors.New("handle unavailable"), gpu.MeasurementSignals()...)
		m.SetEntityKey(health.ProvisionalEntity(source, "0"))
		return m
	}
	a := cadenceRunApp().WithNodeID(testNodeID).WithGPUBackend(toolkit.MockGPU(slot("nvml"), slot("dcgm")))

	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitUnhealthy {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, exitUnhealthy, stderr.String())
	}
	var verdict health.NodeVerdict
	if err := json.Unmarshal(stdout.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	var entities []string
	for _, v := range verdict.CheckResults {
		if v.CheckID == "gpu_power_state" {
			entities = append(entities, v.AffectedEntities...)
		}
	}
	if !slices.Equal(entities, []string{"nvml-index-0", "dcgm-index-0"}) {
		t.Fatalf("gpu_power_state entities = %v, want one failure per source slot", entities)
	}
}
