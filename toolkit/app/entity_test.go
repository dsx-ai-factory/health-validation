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

// shadowAdapter is a caller adapter that reads the custom count but builds ms,
// measurements of a built-in category. It declares a field no built-in
// adapter feeds, so registration accepts it.
type shadowAdapter struct {
	name     string
	ms       []health.Measurement
	category health.Category
}

func (a shadowAdapter) Name() string              { return a.name }
func (a shadowAdapter) Category() health.Category { return a.category }
func (shadowAdapter) Signals() map[string][]string {
	return map[string][]string{customCountSignal: {"shadow_count"}}
}
func (a shadowAdapter) Build([]*observation.Observation) []health.Measurement { return a.ms }
func (shadowAdapter) Errored(error) []health.Measurement                      { return nil }

// withShadow registers adapter, fed by the custom count sensor, and a check
// reading its field so a run builds it.
func withShadow(a *App, adapter CollectAdapter) *App {
	sensor := &collectAppSensor{}
	reader := health.CheckDefinition{
		ID: "shadow_reader", Name: "Shadow reader", Category: adapter.Category(), RequiredSignals: []string{"shadow_count"},
		Rules: []health.Rule{{Expression: "shadow_count < 0", Severity: health.Critical}},
	}
	return a.WithNodeID(testNodeID).WithChecks(reader).WithCollect(sensor, claimAll(sensor)...).WithCollectAdapters(adapter)
}

// Two measurements of one GPU are a wiring fault, not two findings: the CLI
// reports neither and exits 2.
func TestRunRejectsDuplicateGPUMeasurements(t *testing.T) {
	a := withShadow(cadenceRunApp(), shadowAdapter{name: "shadow-gpu", category: health.GPU, ms: []health.Measurement{
		gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 8},
	}})
	runAmbiguous(t, a, `check "gpu_`, `entity "GPU-0"`, "gpu.GPUMeasurement, gpu.GPUMeasurement")
}

// A caller adapter feeding a field an active built-in adapter also feeds is
// rejected at registration, before anything is measured.
func TestRunRejectsCallerAdapterDuplicatingBuiltinCategory(t *testing.T) {
	a := withShadow(cadenceRunApp(), shadowCPUAdapter{})
	var stdout, stderr bytes.Buffer
	if code := a.Run([]string{"run"}, &stdout, &stderr); code != exitError || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := `collect adapter "shadow-cpu" feeds cpu field "online_cpu_count", which the built-in "cpu" adapter also reports`; !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr=%q, want %q", stderr.String(), want)
	}
}

// shadowCPUAdapter declares a field the built-in CPU adapter feeds.
type shadowCPUAdapter struct{ shadowAdapter }

func (shadowCPUAdapter) Name() string              { return "shadow-cpu" }
func (shadowCPUAdapter) Category() health.Category { return health.CPU }
func (shadowCPUAdapter) Signals() map[string][]string {
	return map[string][]string{customCountSignal: {cpu.SignalOnlineCPUCount}}
}

// An adapter that passes registration but builds a measurement the built-in
// adapter also builds is caught by the check/entity guard.
func TestRunRejectsCallerAdapterBuildingBuiltinMeasurement(t *testing.T) {
	a := withShadow(cadenceRunApp(), shadowAdapter{name: "shadow-cpu", category: health.CPU, ms: []health.Measurement{
		cpu.CPUMeasurement{PossibleCPUCount: 1, OnlineCPUCount: 1, NUMANodeCount: 34},
	}})
	runAmbiguous(t, a, `check "cpu_count", node`)
}

// NVML's slot 0 and DCGM's slot 0 failing are two unresolved GPUs. The run
// still produces a verdict, failing each.
func TestRunKeepsSameSlotFromTwoSourcesDistinct(t *testing.T) {
	slot := func(source string) health.Measurement {
		m := gpu.ErroredGPU(source+"-index-0", errors.New("handle unavailable"), gpu.MeasurementSignals()...)
		m.SetEntityKey(health.ProvisionalEntity(source, "0"))
		return m
	}
	a := withShadow(cadenceRunApp(), shadowAdapter{name: "slots", category: health.GPU, ms: []health.Measurement{slot("nvml"), slot("dcgm")}})

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
