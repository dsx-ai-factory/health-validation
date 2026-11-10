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

// TestGPUPCIeMaxLink checks architecture defaults and mismatches in either link property.
func TestGPUPCIeMaxLink(t *testing.T) {
	for _, tc := range []struct {
		arch       string
		generation int64
	}{
		{"A100", 4}, {"H100", 5}, {"H200", 5}, {"B200", 5}, {"GB200", 5}, {"GB300", 6}, {"VR200", 6},
	} {
		t.Run(tc.arch, func(t *testing.T) {
			cat := catalog.New(catalog.WithArchitecture(tc.arch))
			for _, scenario := range []struct {
				name              string
				width, generation int64
				want              health.Severity
			}{
				{"matching", 16, tc.generation, health.Pass},
				{"narrow", 8, tc.generation, health.Critical},
				{"wide", 32, tc.generation, health.Critical},
				{"lower generation", 16, tc.generation - 1, health.Critical},
				{"higher generation", 16, tc.generation + 1, health.Critical},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					v, err := cat.Evaluate("gpu_pcie_max_link", gpu.GPUMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: scenario.width, PCIeMaxLinkGeneration: scenario.generation})
					if err != nil {
						t.Fatal(err)
					}
					if v.Skipped || v.Severity != scenario.want {
						t.Fatalf("verdict = %+v, want %v", v, scenario.want)
					}
					if scenario.want == health.Critical && !strings.Contains(strings.Join(v.Messages, " "), "GPU-0") {
						t.Fatalf("missing GPU identity: %+v", v)
					}
				})
			}
		})
	}
}

// TestGPUPCIeMaxLinkOverrideAndCollectionFailure separates operator expectations from read failures.
func TestGPUPCIeMaxLinkOverrideAndCollectionFailure(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("H100"), catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "gpu_pcie_max_link", ThresholdName: "expected_pcie_max_link_width", Value: int64(8)},
		catalog.Override{CheckID: "gpu_pcie_max_link", ThresholdName: "expected_pcie_max_link_generation", Value: int64(4)},
	)))
	m := gpu.GPUMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: 8, PCIeMaxLinkGeneration: 4}
	v, err := cat.Evaluate("gpu_pcie_max_link", m)
	if err != nil || v.Severity != health.Pass {
		t.Fatalf("override: %+v, %v", v, err)
	}
	for _, signal := range []string{gpu.SignalPCIeMaxLinkWidth, gpu.SignalPCIeMaxLinkGeneration} {
		failed := m
		failed.AddError(errors.New("PCIe query failed"), signal)
		v, err = cat.Evaluate("gpu_pcie_max_link", failed)
		if err != nil || v.Severity != health.Unknown {
			t.Fatalf("collection failure: %+v, %v", v, err)
		}
	}
}

// TestGPUPCIeMaxLinkUnavailableValues keeps partial or invalid data from reporting a hardware fault.
func TestGPUPCIeMaxLinkUnavailableValues(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"))
	for _, tc := range []struct {
		name              string
		width, generation int64
	}{
		{"both omitted", 0, 0},
		{"width omitted", 0, 5},
		{"generation omitted", 16, 0},
		{"width omitted with generation mismatch", 0, 4},
		{"generation omitted with width mismatch", 8, 0},
		{"negative width", -1, 5},
		{"negative generation", 16, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := cat.Evaluate("gpu_pcie_max_link", gpu.GPUMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: tc.width, PCIeMaxLinkGeneration: tc.generation})
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped || v.Severity != health.Unknown {
				t.Fatalf("verdict = %+v, want UNKNOWN", v)
			}
			if !v.RecommendedAction.Equal(health.Drain(health.RemediationActionCheckCollection)) {
				t.Fatalf("action = %+v, want check_collection", v.RecommendedAction)
			}
		})
	}
}
