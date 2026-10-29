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
		ConditionScope:    health.NodeScope,
		Provenance:        health.NVIDIA,
		MaturityLevel:     health.Experimental,
		RecommendedAction: health.Drain(health.RemediationActionServiceRequired),
		Rules: []health.Rule{{
			Expression: "size(acs_failures) > 0 || size(iommu_group_failures) > 0",
			Severity:   health.Critical,
			Message:    "rdma_topo reported ACS or IOMMU group configuration failures. See acs_failures and iommu_group_failures for diagnostics.",
		}},
	}
}
