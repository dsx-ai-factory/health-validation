// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvlink_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

func healthyFabric() nvlink.NVLinkMeasurement {
	return nvlink.NVLinkMeasurement{GPUID: "GPU-0"}
}

func fabricCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

func TestNVLinkFabricHealth(t *testing.T) {
	tests := []struct {
		cat                 func() *catalog.Catalog
		measurement         func() nvlink.NVLinkMeasurement
		name                string
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
			name: "bandwidth degraded",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyFabric()
				m.FabricBandwidthDegraded = true
				return m
			},
			wantSeverity: health.Critical,
		},
		{
			name: "route recovery in progress",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyFabric()
				m.FabricRouteRecoveryInProgress = true
				return m
			},
			wantSeverity: health.Critical,
		},
		{
			name: "route unhealthy",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyFabric()
				m.FabricRouteUnhealthy = true
				return m
			},
			wantSeverity: health.Critical,
		},
		{
			name: "access timeout recovery",
			cat:  func() *catalog.Catalog { return fabricCatalog() },
			measurement: func() nvlink.NVLinkMeasurement {
				m := healthyFabric()
				m.FabricAccessTimeoutRecovery = true
				return m
			},
			wantSeverity: health.Critical,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.cat().Evaluate("nvlink_fabric_health", tt.measurement())
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
			if tt.checkNVIDIAEndorsed && !v.NVIDIAEndorsed {
				t.Error("expected nvidia_endorsed = true")
			}
		})
	}
}
