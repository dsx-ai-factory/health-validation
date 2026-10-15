// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func computeAppsCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.TenantGuest),
	}
	c := catalog.NewEmpty(append(defaults, opts...)...)
	c.MustRegister(gpu.GPUComputeApps())
	return c
}

func TestGPUComputeApps_EmptyPasses(t *testing.T) {
	v, err := computeAppsCatalog().Evaluate("gpu_compute_apps", gpu.GPUMeasurement{GPUID: "GPU-0"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Pass {
		t.Errorf("Severity = %v, want PASS", v.Severity)
	}
	if !v.RecommendedAction.IsZero() {
		t.Errorf("RecommendedAction = %v, want zero on PASS", v.RecommendedAction)
	}
}

func TestGPUComputeApps_ProcessFails(t *testing.T) {
	m := gpu.GPUMeasurement{
		GPUID:                  "GPU-0",
		ComputeProcessCmdlines: "pid=12345 /usr/bin/python train.py",
		ComputeProcessCount:    1,
	}
	v, err := computeAppsCatalog().Evaluate("gpu_compute_apps", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := health.Drain(health.RemediationActionRestartOS, health.RemediationActionServiceRequired)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
	msg := strings.Join(v.Messages, " ")
	for _, part := range []string{"GPU-0", "12345", "train.py"} {
		if !strings.Contains(msg, part) {
			t.Errorf("Messages = %q, want %q", v.Messages, part)
		}
	}
}

func TestGPUComputeApps_MultipleProcesses(t *testing.T) {
	m := gpu.GPUMeasurement{
		GPUID:                  "GPU-1",
		ComputeProcessCmdlines: "pid=1 python\npid=2 python3",
		ComputeProcessCount:    2,
	}
	v, err := computeAppsCatalog().Evaluate("gpu_compute_apps", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	msg := strings.Join(v.Messages, "\n")
	if !strings.Contains(msg, "2 compute") {
		t.Errorf("Messages = %q, want process count 2", v.Messages)
	}
	if !strings.Contains(msg, "pid=1 python") || !strings.Contains(msg, "pid=2 python3") {
		t.Errorf("Messages = %q, want both process lines", v.Messages)
	}
}

func TestGPUComputeApps_ErroredSignal(t *testing.T) {
	m := gpu.GPUMeasurement{GPUID: "GPU-0"}
	m.AddError(errors.New("nvml compute process query failed"), gpu.SignalComputeProcessCount)
	v, err := computeAppsCatalog().Evaluate("gpu_compute_apps", m)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if v.Severity != health.Critical {
		t.Errorf("Severity = %v, want CRITICAL", v.Severity)
	}
	want := health.Drain(health.RemediationActionCheckCollection)
	if !v.RecommendedAction.Equal(want) {
		t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, want)
	}
}

func TestGPUComputeApps_OperatorInfraSkipped(t *testing.T) {
	cat := computeAppsCatalog(catalog.WithExecutionContext(health.OperatorInfra))
	v, err := cat.Evaluate("gpu_compute_apps", gpu.GPUMeasurement{GPUID: "GPU-0"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.DeploymentUnsupported {
		t.Errorf("skipped = %v, reason = %v, want DeploymentUnsupported", v.Skipped, v.SkipReason)
	}
}

func TestGPUComputeApps_WrongArchitectureSkipped(t *testing.T) {
	cat := computeAppsCatalog(catalog.WithArchitecture("T4"))
	v, err := cat.Evaluate("gpu_compute_apps", gpu.GPUMeasurement{GPUID: "GPU-0"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("skipped = %v, reason = %v, want ArchitectureUnsupported", v.Skipped, v.SkipReason)
	}
}
