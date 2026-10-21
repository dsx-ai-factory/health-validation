// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	xidsignals "github.com/dsx-ai-factory/health-validation/collect/signals/xid"
)

// XID replaces toolkit.XidSensor for the stock CLI's Xid domain.
func XID() *domainAdapter {
	return newDomainAdapter(health.Xid, erroredXid, true)
}

func erroredXid(err error) []health.Measurement {
	return []health.Measurement{xid.ErroredXid(xidsignals.WindowBoot, err, xid.MeasurementSignals()...)}
}
