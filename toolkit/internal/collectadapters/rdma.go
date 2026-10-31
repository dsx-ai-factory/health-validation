// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectadapters

import (
	"errors"
	"fmt"
	"maps"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

// rdmaPortFeeds maps the infiniband.sysfs port signals onto the
// PortMeasurement fields they fill.
var rdmaPortFeeds = map[string][]string{
	ibsignals.State.ID():         {infiniband.SignalPortStates},
	ibsignals.PhysicalState.ID(): {infiniband.SignalPortPhysicalStates},
	ibsignals.RateGbps.ID():      {infiniband.SignalPortRatesGbps},
	ibsignals.LinkLayer.ID():     {infiniband.SignalPortLinkLayers},
}

// rdmaPortsAdapter builds the node-wide [infiniband.PortMeasurement] from
// per-port sysfs observations. It is not a [domainAdapter]: that groups by
// GPU, while this measurement is one map per signal keyed by port.
type rdmaPortsAdapter struct{}

// RDMAPorts assembles the RDMA fabric port measurement. Pair it with the
// infiniband.sysfs sensor built with WithEthernetPorts, so RoCE rails are
// reported alongside InfiniBand ones.
func RDMAPorts() *rdmaPortsAdapter { return &rdmaPortsAdapter{} }

// Name is the catalog category, which app reserves for the built-in adapter.
func (*rdmaPortsAdapter) Name() string { return health.InfiniBand.String() }

func (*rdmaPortsAdapter) Category() health.Category { return health.InfiniBand }

func (*rdmaPortsAdapter) Signals() map[string][]string { return maps.Clone(rdmaPortFeeds) }

// Build consumes observations already validated by the bridge.
//
// A value that cannot be read for one port leaves that port out of the
// affected map, so it is never counted as healthy; whether that matters is
// the check's call, since an unreadable storage port is not an unreadable
// fabric rail. A failure that names no port is a failure of the whole
// source and marks the signal failed for every check that reads it.
func (a *rdmaPortsAdapter) Build(obs []*observation.Observation) []health.Measurement {
	m := infiniband.PortMeasurement{
		States:         map[string]int64{},
		PhysicalStates: map[string]int64{},
		RatesGbps:      map[string]float64{},
		LinkLayers:     map[string]string{},
	}
	seen := false
	for _, o := range obs {
		catalogSignals, ok := rdmaPortFeeds[o.GetSignalId()]
		if !ok {
			continue
		}
		seen = true
		ref := o.GetTarget().GetInfiniBandPort()
		if ref == nil {
			if failure := o.GetCollectionError(); failure != nil {
				m.AddError(rdmaCollectionFailure(failure), catalogSignals...)
			}
			continue
		}
		value := o.GetValue()
		if value == nil {
			continue
		}
		key := fmt.Sprintf("%s/%d", ref.GetDevice().GetName(), ref.GetPort())
		switch o.GetSignalId() {
		case ibsignals.State.ID():
			if v, ok := value.GetKind().(*observation.Value_IntValue); ok {
				m.States[key] = v.IntValue
			}
		case ibsignals.PhysicalState.ID():
			if v, ok := value.GetKind().(*observation.Value_IntValue); ok {
				m.PhysicalStates[key] = v.IntValue
			}
		case ibsignals.RateGbps.ID():
			if v, ok := value.GetKind().(*observation.Value_DoubleValue); ok {
				m.RatesGbps[key] = v.DoubleValue
			}
		case ibsignals.LinkLayer.ID():
			if v, ok := value.GetKind().(*observation.Value_StringValue); ok {
				m.LinkLayers[key] = v.StringValue
			}
		}
	}
	if !seen {
		return a.Errored(errors.New("no RDMA port observations were collected"))
	}
	return []health.Measurement{m}
}

func (*rdmaPortsAdapter) Errored(err error) []health.Measurement {
	return []health.Measurement{infiniband.ErroredPorts(err)}
}

// rdmaCollectionFailure renders a source-level collection error the way
// observations.ToMeasurements does for mapped domains.
func rdmaCollectionFailure(failure *observation.CollectionError) error {
	msg := fmt.Sprintf("%s operation %q failed with %s", failure.GetSource(), failure.GetOperation(), failure.GetCategory())
	if failure.GetSourceError() != "" {
		msg += fmt.Sprintf(" (%s)", failure.GetSourceError())
	}
	if failure.GetDetail() != "" {
		msg += ": " + failure.GetDetail()
	}
	if failure.GetCategory() == observation.CollectionErrorCategoryTimeout {
		return fmt.Errorf("%s: %w", msg, health.ErrCollectionTimeout)
	}
	return errors.New(msg)
}
