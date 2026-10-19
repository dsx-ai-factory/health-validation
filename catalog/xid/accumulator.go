// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// SignalSubXidCodeCounts is the signal an [XidDerivedMeasurement] carries.
const SignalSubXidCodeCounts = "sub_xid_code_counts"

var derivedMeasurementSignals = []string{SignalSubXidCodeCounts}

// DerivedMeasurementSignals lists every signal an XidDerivedMeasurement
// carries. The returned slice is a copy.
func DerivedMeasurementSignals() []string { return slices.Clone(derivedMeasurementSignals) }

// XidDerivedMeasurement holds what the catalog reads out of an
// [XidMeasurement]'s events, as opposed to what a sensor collected: which
// fault subtype each NVLink5 event is (sub_xid_code_counts). Only the
// [Accumulator] produces it.
type XidDerivedMeasurement struct {
	SubXidCodeCounts map[string]int64 `json:"sub_xid_code_counts"`
	errors           []health.ErroredMeasurement
}

// Category implements [health.Measurement].
func (XidDerivedMeasurement) Category() health.Category { return health.Xid }

// Errors implements [health.Measurement].
func (m XidDerivedMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// Accumulator derives an [XidDerivedMeasurement] from each
// [XidMeasurement] a run collected, joined to the node's driver version
// from its [node.NodeMeasurement] when one was collected too.
//
// Deriving in the catalog rather than the sensor means every source of Xid
// events, whatever produced them, is interpreted the same way.
type Accumulator struct{}

var _ health.Accumulator = Accumulator{}

// Name implements [health.Accumulator].
func (Accumulator) Name() string { return "xid" }

// Outputs implements [health.Accumulator].
func (Accumulator) Outputs() []health.Measurement {
	return []health.Measurement{XidDerivedMeasurement{}}
}

// Inputs implements [health.Accumulator].
func (Accumulator) Inputs() []health.Measurement {
	return []health.Measurement{XidMeasurement{}, node.NodeMeasurement{}}
}

// Accumulate implements [health.Accumulator].
func (Accumulator) Accumulate(measurements []health.Measurement) ([]health.Measurement, error) {
	driverVersion := collectedDriverVersion(measurements)
	var out []health.Measurement
	for _, m := range measurements {
		if x, ok := m.(XidMeasurement); ok {
			out = append(out, derive(x, driverVersion))
		}
	}
	return out, nil
}

// collectedDriverVersion is the driver version the run collected, or the
// zero value, meaning unknown, if none was collected or it failed to read.
// An unknown version never fails a derivation.
func collectedDriverVersion(measurements []health.Measurement) health.DriverVersion {
	for _, m := range measurements {
		if n, ok := m.(node.NodeMeasurement); ok {
			return n.ParsedDriverVersion()
		}
	}
	return health.DriverVersion{}
}

// derive computes x's derived signals. driverVersion is for derivations
// whose meaning changed between driver releases; sub_xid_code_counts is
// not one, since IntrInfo[25:20] sits in the same place in every era.
func derive(x XidMeasurement, _ health.DriverVersion) XidDerivedMeasurement {
	// Every derived signal is read from the events. If they could not be
	// collected, report the derived signals failed rather than empty: an
	// empty count reads as "no fault" and the checks would report PASS.
	for _, e := range x.Errors() {
		for _, sig := range e.FailedSignals() {
			if sig == SignalEvents {
				var d XidDerivedMeasurement
				d.errors = append(d.errors, health.NewErroredMeasurement(e.Err(), DerivedMeasurementSignals()...))
				return d
			}
		}
	}
	return XidDerivedMeasurement{SubXidCodeCounts: subXidCodeCounts(x.Events)}
}
