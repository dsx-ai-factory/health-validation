// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
)

// RDMATopo assembles rdma_topo findings and collection failures for the CLI.
func RDMATopo() *domainAdapter {
	return newDomainAdapter(health.PCIe, erroredRDMATopo, true)
}

func erroredRDMATopo(err error) []health.Measurement {
	return []health.Measurement{pcie.ErroredRDMATopo(err)}
}
