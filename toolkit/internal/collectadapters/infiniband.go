// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

// ibPortsAdapter feeds both InfiniBand port measurement types from the sysfs
// port sensor: one infiniband.PortMeasurement per port, plus the node-wide
// infiniband.PortInventoryMeasurement derived from them. The journal link-flap
// sensor still supplies the category's other type.
type ibPortsAdapter struct{ perPort *domainAdapter }

// InfiniBand feeds the sysfs port measurements. Finding no ports is a
// collection failure like any other domain's: a missing driver or the
// device filter can leave sysfs empty, so absence cannot be read as "nothing
// to check". Deployments without InfiniBand disable the port checks instead.
func InfiniBand() *ibPortsAdapter {
	return &ibPortsAdapter{perPort: newDomainAdapter(health.InfiniBand, erroredInfiniBand, false)}
}

// Name is the catalog category, which app reserves for the built-in adapter.
func (a *ibPortsAdapter) Name() string { return a.perPort.Name() }

func (a *ibPortsAdapter) Category() health.Category { return a.perPort.Category() }

// Signals also maps the rate onto the inventory's signal, so signal narrowing
// requests it when only the inventory's check will run.
func (a *ibPortsAdapter) Signals() map[string][]string {
	feeds := a.perPort.Signals()
	rate := ibsignals.RateGbps.ID()
	feeds[rate] = append(feeds[rate], infiniband.SignalPortRatesGbps)
	return feeds
}

func (a *ibPortsAdapter) Build(obs []*observation.Observation) []health.Measurement {
	built := a.perPort.Build(obs)
	ports := make([]infiniband.PortMeasurement, 0, len(built))
	for _, m := range built {
		if p, ok := m.(infiniband.PortMeasurement); ok {
			ports = append(ports, p)
		}
	}
	return append(built, infiniband.InventoryOf(ports))
}

func (a *ibPortsAdapter) Errored(err error) []health.Measurement {
	return append(a.perPort.Errored(err), infiniband.ErroredPortInventory(err))
}
