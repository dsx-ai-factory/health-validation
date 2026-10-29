// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package rdmatopo defines signals collected by rdma_topo check.
package rdmatopo

import "github.com/dsx-ai-factory/health-validation/collect"

var (
	ACSFailures        = collect.NewSignal("pcie.rdma_topo.acs_failures", "")
	IOMMUGroupFailures = collect.NewSignal("pcie.rdma_topo.iommu_group_failures", "")
	Evidence           = collect.NewSignal("pcie.rdma_topo.evidence", "")
)

// All returns the rdma_topo signals in stable order.
func All() collect.Signals {
	return collect.Signals{ACSFailures, IOMMUGroupFailures, Evidence}
}
