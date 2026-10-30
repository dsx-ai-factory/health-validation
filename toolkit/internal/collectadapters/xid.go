// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
)

// XID replaces toolkit.XidSensor for the stock CLI's Xid domain. gpuUUIDs is
// read each time the adapter builds, after the run's other sensors measured;
// it maps PCI addresses in the kernel's Xid form to GPU UUIDs. Nil leaves
// every GPU named by its address.
func XID(gpuUUIDs func() map[string]string) *domainAdapter {
	a := newDomainAdapter(health.Xid, erroredXid, true)
	a.gpuUUIDs = gpuUUIDs
	return a
}

func erroredXid(err error) []health.Measurement {
	return []health.Measurement{xid.ErroredXid(xidsignals.WindowBoot, err, xid.MeasurementSignals()...)}
}
