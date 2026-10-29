// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// RDMATopo detects ACS and IOMMU group configuration failures reported by rdma_topo.
func RDMATopo() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "rdma_topo",
		Name:              "rdma_topo",
		Description:       "Detects ACS and IOMMU group configuration failures reported by rdma_topo check.",
		Category:          health.PCIe,
		RequiredSignals:   MeasurementSignals(),
		ExecutionClass:    health.Passive,
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra, health.TenantGuest},
		SchedulingCadence: health.Prolog,
		Architectures:     []string{"GB200", "GB300", "B200", "B300", "VR"},
		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Stable,
		RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
		Rules: []health.Rule{{
			Expression: "size(acs_failures) > 0 || size(iommu_group_failures) > 0",
			Severity:   health.Critical,
			Message:    "rdma_topo reported ACS or IOMMU group configuration failures. In a VM, contact your infrastructure operator. On bare metal, follow the platform guidance to verify NIC ATS settings and correct the reported configuration (administrative access is required), then rerun.",
		}},
	}
}
