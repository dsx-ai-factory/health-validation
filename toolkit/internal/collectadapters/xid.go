// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"log/slog"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
)

// XID replaces toolkit.XidSensor for the stock CLI's Xid domain. gpuUUIDs is
// read each time the adapter builds; it maps PCI addresses in the kernel's
// Xid form to GPU UUIDs. Nil leaves every GPU named by its address.
func XID(gpuUUIDs func() map[string]string) *xidAdapter {
	return &xidAdapter{domainAdapter: newDomainAdapter(health.Xid, erroredXid), gpuUUIDs: gpuUUIDs}
}

type xidAdapter struct {
	*domainAdapter
	gpuUUIDs func() map[string]string
}

// Build names each GPU by UUID when its normalized PCI address is a key, and
// warns about a GPU that logged an Xid but is named only by its address.
func (a *xidAdapter) Build(obs []*observation.Observation) []health.Measurement {
	out := a.domainAdapter.Build(obs)
	if a.gpuUUIDs == nil {
		return out
	}
	uuids := a.gpuUUIDs()
	for i, m := range out {
		if x, ok := m.(xid.XidMeasurement); ok && x.PCIAddress != "" {
			if x.GPUID = uuids[x.PCIAddress]; x.GPUID == "" {
				slog.Warn("no GPU UUID for Xid address; results name the GPU by PCI address", "pci_address", x.PCIAddress)
			}
			out[i] = x
		}
	}
	return out
}

func erroredXid(err error) []health.Measurement {
	return []health.Measurement{xid.ErroredXid(xidsignals.WindowBoot, err, xid.MeasurementSignals()...)}
}
