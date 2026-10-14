// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

func TestNVLinkFabric(t *testing.T) {
	tests := []struct {
		cat                 func() *catalog.Catalog
		measurement         func() nvlink.NVLinkMeasurement
		name                string
		wantMessageParts    []string
		wantAction          health.RecommendedAction
		wantSeverity        health.Severity
		wantSkipReason      health.SkipReason
		wantSkipped         bool
		checkNVIDIAEndorsed bool
	}{
		{
			name:                "healthy",
			cat:                 func() *catalog.Catalog { return fabricCatalog() },
			measurement:         healthyFabric,
			wantSeverity:        health.Pass,
			checkNVIDIAEndorsed: true,
		},
		{
			name: "registration incomplete",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyFabric()
				m.FabricRegistrationIncomplete = true
				return m
			},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0"},
		},
		{
			name: "registration failed",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyFabric()
				m.FabricRegistrationFailed = true
				return m
			},
			wantSeverity:     health.Critical,
			wantMessageParts: []string{"GPU-0"},
		},
		{
			name: "skipped on wrong architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("H100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement:    healthyFabric,
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			name: "errored GPU forces critical",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.ErroredNVLink("GPU-0", errors.New("nvml fabric query failed"),
					nvlink.SignalFabricRegistrationIncomplete, nvlink.SignalFabricRegistrationFailed)
			},
			wantSeverity: health.Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("nvlink_fabric", tt.measurement())
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped != tt.wantSkipped || v.SkipReason != tt.wantSkipReason {
				t.Fatalf("skipped=%v reason=%v, want skipped=%v reason=%v", v.Skipped, v.SkipReason, tt.wantSkipped, tt.wantSkipReason)
			}
			if tt.wantSkipped {
				return
			}
			if v.Severity != tt.wantSeverity {
				t.Fatalf("severity = %v, want %v", v.Severity, tt.wantSeverity)
			}
			if checkAction := !tt.wantAction.IsZero(); checkAction && !v.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("recommended_action = %+v, want %+v", v.RecommendedAction, tt.wantAction)
			}
			if tt.checkNVIDIAEndorsed && !v.NVIDIAEndorsed {
				t.Error("expected nvidia_endorsed = true")
			}
			msg := strings.Join(v.Messages, " ")
			for _, want := range tt.wantMessageParts {
				if !strings.Contains(msg, want) {
					t.Errorf("Messages = %q, want %q interpolated", v.Messages, want)
				}
			}
		})
	}
}
