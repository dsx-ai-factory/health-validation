// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

// TestGPUPCIeMaxLink checks exact widths and minimum generations for each architecture.
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
				{"higher generation", 16, tc.generation + 1, health.Pass},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					v, err := cat.Evaluate("gpu_pcie_max_link", pcie.PCIeMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: scenario.width, PCIeMaxLinkGeneration: scenario.generation})
					if err != nil {
						t.Fatal(err)
					}
					if v.Category != health.PCIe || v.Skipped || v.Severity != scenario.want {
						t.Fatalf("verdict = %+v, want %v", v, scenario.want)
					}
					if scenario.want == health.Critical && !v.RecommendedAction.Equal(health.GenericDrain()) {
						t.Fatalf("critical action = %+v, want drain", v.RecommendedAction)
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
	m := pcie.PCIeMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: 8, PCIeMaxLinkGeneration: 4}
	for _, generation := range []int64{3, 4, 5} {
		observed := m
		observed.PCIeMaxLinkGeneration = generation
		v, err := cat.Evaluate("gpu_pcie_max_link", observed)
		want := health.Pass
		if generation < 4 {
			want = health.Critical
		}
		if err != nil || v.Skipped || v.Severity != want {
			t.Fatalf("generation %d with minimum override 4: %+v, %v", generation, v, err)
		}
	}
	for _, signal := range []string{pcie.SignalPCIeMaxLinkWidth, pcie.SignalPCIeMaxLinkGeneration} {
		failed := m
		failed.AddError(errors.New("PCIe query failed"), signal)
		v, err := cat.Evaluate("gpu_pcie_max_link", failed)
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
		{"negative width", -2, 5},
		{"negative generation", 16, -2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := cat.Evaluate("gpu_pcie_max_link", pcie.PCIeMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: tc.width, PCIeMaxLinkGeneration: tc.generation})
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

// TestGPUPCIeMaxLinkUnsupported preserves measurable failures when another
// capability is unsupported; missing values and read failures remain UNKNOWN.
func TestGPUPCIeMaxLinkUnsupported(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("H100"))
	for _, tc := range []struct {
		name              string
		failure           error
		action            health.RecommendedAction
		width, generation int64
		want              health.Severity
	}{
		{name: "unsupported width", width: pcie.UnsupportedLinkValue, generation: 5, want: health.Info},
		{name: "unsupported generation", width: 16, generation: pcie.UnsupportedLinkValue, want: health.Info},
		{name: "both unsupported", width: pcie.UnsupportedLinkValue, generation: pcie.UnsupportedLinkValue, want: health.Info},
		{name: "unsupported width with generation mismatch", width: pcie.UnsupportedLinkValue, generation: 4, want: health.Critical, action: health.GenericDrain()},
		{name: "unsupported generation with narrow width", width: 8, generation: pcie.UnsupportedLinkValue, want: health.Critical, action: health.GenericDrain()},
		{name: "unsupported generation with wide width", width: 32, generation: pcie.UnsupportedLinkValue, want: health.Critical, action: health.GenericDrain()},
		{name: "unsupported width with higher generation", width: pcie.UnsupportedLinkValue, generation: 6, want: health.Info},
		{name: "unsupported generation with missing width", generation: pcie.UnsupportedLinkValue, want: health.Unknown, action: health.Drain(health.RemediationActionCheckCollection)},
		{name: "unsupported generation with invalid width", width: -2, generation: pcie.UnsupportedLinkValue, want: health.Unknown, action: health.Drain(health.RemediationActionCheckCollection)},
		{name: "unsupported and missing", width: pcie.UnsupportedLinkValue, want: health.Unknown, action: health.Drain(health.RemediationActionCheckCollection)},
		{name: "unsupported and invalid", width: pcie.UnsupportedLinkValue, generation: -2, want: health.Unknown, action: health.Drain(health.RemediationActionCheckCollection)},
		{name: "read failure", width: pcie.UnsupportedLinkValue, failure: errors.New("read failed"), want: health.Unknown, action: health.Drain(health.RemediationActionCheckCollection)},
		{name: "read failure with width mismatch", width: 8, generation: 5, failure: errors.New("read failed"), want: health.Unknown, action: health.Drain(health.RemediationActionCheckCollection)},
		{name: "timeout", width: pcie.UnsupportedLinkValue, failure: health.ErrCollectionTimeout, want: health.Unknown, action: health.Drain(health.RemediationActionRetryCollection)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := pcie.PCIeMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: tc.width, PCIeMaxLinkGeneration: tc.generation}
			if tc.failure != nil {
				m.AddError(tc.failure, pcie.SignalPCIeMaxLinkGeneration)
			}
			v, err := cat.Evaluate("gpu_pcie_max_link", m)
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped || v.Severity != tc.want || !v.RecommendedAction.Equal(tc.action) {
				t.Fatalf("verdict = %+v, want %v with %+v", v, tc.want, tc.action)
			}
		})
	}
}

// TestGPUPCIeMaxLinkGuest evaluates exposed capabilities and unsupported values.
func TestGPUPCIeMaxLinkGuest(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.TenantGuest))
	for _, tc := range []struct {
		width int64
		want  health.Severity
	}{
		{16, health.Pass}, {8, health.Critical}, {pcie.UnsupportedLinkValue, health.Info},
	} {
		v, err := cat.Evaluate("gpu_pcie_max_link", pcie.PCIeMeasurement{GPUID: "GPU-0", PCIeMaxLinkWidth: tc.width, PCIeMaxLinkGeneration: 5})
		if err != nil || v.Skipped || v.Severity != tc.want {
			t.Fatalf("guest verdict=%+v err=%v, want %v", v, err, tc.want)
		}
	}
}
