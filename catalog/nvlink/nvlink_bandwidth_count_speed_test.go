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

func healthyLinks() nvlink.NVLinkMeasurement {
	return nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 18, SpeedMBytesPerSec: 53125}
}

func TestNVLinkBandwidthCountSpeed(t *testing.T) {
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
			name:         "healthy",
			cat:          func() *catalog.Catalog { return fabricCatalog() },
			measurement:  healthyLinks,
			wantSeverity: health.Pass,
		},
		{
			name: "link shortfall",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyLinks()
				m.LinkCount = 17
				return m
			},
			wantSeverity:     health.Critical,
			wantMessageParts: []string{"GPU-0", "18", "17"},
		},
		{
			name: "speed mismatch",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyLinks()
				m.SpeedMBytesPerSec = 50000
				return m
			},
			wantSeverity:     health.Critical,
			wantAction:       health.GenericDrain(),
			wantMessageParts: []string{"GPU-0", "53125", "50000"},
		},
		{
			// Both thresholds are DefaultThreshold, so an operator override
			// shadows the built-in GB200 default. Observed matches the
			// overrides -- the defaults (18, 53125) would have fired both
			// rules, so passing demonstrates both applied.
			name: "override expected link count and speed",
			cat: func() *catalog.Catalog {
				return fabricCatalog(catalog.WithOverrides(catalog.NewOverrideSet(
					catalog.Override{
						CheckID:       "nvlink_bandwidth_count_speed",
						ThresholdName: "expected_link_count",
						Value:         int64(12),
					},
					catalog.Override{
						CheckID:       "nvlink_bandwidth_count_speed",
						ThresholdName: "expected_speed_mbytes_per_sec",
						Value:         int64(26562),
					},
				)))
			},
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 12, SpeedMBytesPerSec: 26562}
			},
			wantSeverity: health.Pass,
		},
		{
			name: "skipped on wrong architecture",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("A100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement:    healthyLinks,
			wantSkipped:    true,
			wantSkipReason: health.ArchitectureUnsupported,
		},
		{
			// On H100 the check runs with the per-architecture expected
			// speed (26562), so a healthy H100 passes rather than failing
			// against the GB200 default.
			name: "H100 passes against its own expected speed",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("H100"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 18, SpeedMBytesPerSec: 26562}
			},
			wantSeverity: health.Pass,
		},
		{
			// On GB300 the per-architecture expected speed is 53125 (same
			// as GB200).
			name: "GB300 passes against its own expected speed",
			cat: func() *catalog.Catalog {
				return catalog.New(
					catalog.WithArchitecture("GB300"),
					catalog.WithExecutionContext(health.OperatorInfra),
				)
			},
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.NVLinkMeasurement{GPUID: "GPU-0", LinkCount: 18, SpeedMBytesPerSec: 53125}
			},
			wantSeverity: health.Pass,
		},
		{
			// A GPU whose link count could not be collected propagates to
			// Critical regardless of the rule, since the data the check
			// needs is unavailable.
			name: "errored GPU forces critical",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				return nvlink.ErroredNVLink("GPU-0", errors.New("nvml field query failed"), nvlink.SignalLinkCount)
			},
			wantSeverity: health.Critical,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("nvlink_bandwidth_count_speed", tt.measurement())
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
