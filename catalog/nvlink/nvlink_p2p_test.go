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

func TestNVLinkP2P(t *testing.T) {
	tests := []struct {
		cat              func() *catalog.Catalog
		measurement      func() nvlink.NVLinkMeasurement
		name             string
		wantMessageParts []string
		wantAction       health.RecommendedAction
		wantSeverity     health.Severity
		wantSkipReason   health.SkipReason
		wantSkipped      bool
	}{
		{
			name: "all reachable",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.NVLinkMeasurement{GPUID: "GPU-0", UnhealthyP2PPeerCount: 0}
			},
			wantSeverity: health.Pass,
		},
		{
			name: "one unreachable exceeds default budget",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.NVLinkMeasurement{GPUID: "GPU-0", UnhealthyP2PPeerCount: 1}
			},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0", "1 peer", "allowed: 0"},
		},
		{
			name: "skipped on wrong architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("T4"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.NVLinkMeasurement{GPUID: "GPU-0", UnhealthyP2PPeerCount: 0}
			},
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			// A GPU whose P2P state could not be collected propagates to
			// Critical regardless of the rule, since the data the check
			// needs is unavailable.
			name: "errored GPU forces critical",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.ErroredNVLink("GPU-0", errors.New("nvml p2p status query failed"), nvlink.SignalUnhealthyP2PPeerCount)
			},
			wantSeverity: health.Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("nvlink_p2p", tt.measurement())
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
			if !tt.wantAction.IsZero() && !v.RecommendedAction.Equal(tt.wantAction) {
				t.Errorf("RecommendedAction = %v, want %v", v.RecommendedAction, tt.wantAction)
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
