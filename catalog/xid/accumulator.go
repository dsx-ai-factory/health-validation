// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
)

// derivedSignals are the XidMeasurement signals the [Accumulator] fills in.
var derivedSignals = []string{
	SignalSubXidCodeCounts,
	SignalRecoveryActionCounts,
	SignalUnknownXidCounts,
	SignalInformationalXidCounts,
}

// DerivedSignals lists the XidMeasurement signals the [Accumulator] fills
// in rather than a sensor. The returned slice is a copy.
func DerivedSignals() []string { return slices.Clone(derivedSignals) }

// RefinedSignals implements [health.Refinable].
func (XidMeasurement) RefinedSignals() []string { return DerivedSignals() }

// Accumulator refines each [XidMeasurement] a run collected: it returns
// the measurement with its derived fields filled in from its events,
// joined to the node's driver version from its [node.NodeMeasurement]
// when one was collected too. The refined measurement replaces the
// collected one, so a check reading a derived signal still sees the GPU's
// identity and events.
type Accumulator struct {
	// CoveredCodes names, by check ID, the Xid codes a consumer's own
	// checks cover, beyond the built-in checks. A covered code is not
	// reported by gpu_xid_unknown or gpu_xid_informational, and a check
	// listed here reports only its codes' events. Register the accumulator
	// with [catalog.WithAccumulator] to replace the built-in one.
	CoveredCodes map[string][]int64
}

var (
	_ health.Accumulator = Accumulator{}
	_ health.Refinable   = XidMeasurement{}
)

// Name implements [health.Accumulator].
func (Accumulator) Name() string { return "xid" }

// Outputs implements [health.Accumulator]. XidMeasurement is also an
// input, so the accumulator refines it.
func (Accumulator) Outputs() []health.Measurement {
	return []health.Measurement{XidMeasurement{}}
}

// Inputs implements [health.Accumulator].
func (Accumulator) Inputs() []health.Measurement {
	return []health.Measurement{XidMeasurement{}, node.NodeMeasurement{}}
}

// Derived implements [health.Accumulator].
func (Accumulator) Derived() []string { return DerivedSignals() }

// Accumulate implements [health.Accumulator]. It returns one refined
// measurement per collected XidMeasurement, in order.
func (a Accumulator) Accumulate(measurements []health.Measurement) ([]health.Measurement, error) {
	driverVersion := collectedDriverVersion(measurements)
	var out []health.Measurement
	for _, m := range measurements {
		if x, ok := m.(XidMeasurement); ok {
			out = append(out, refine(x, coverage(a.CoveredCodes), driverVersion))
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

// refine fills in x's derived fields. driverVersion is for derivations
// whose meaning changed between driver releases; none here is one yet.
func refine(x XidMeasurement, covered coverage, _ health.DriverVersion) XidMeasurement {
	// Every derived signal is read from the events. If they could not be
	// collected, the derived signals fail with them rather than reading
	// empty: an empty count reads as "no fault" and the checks would PASS.
	for _, e := range x.Errors() {
		if slices.Contains(e.FailedSignals(), SignalEvents) && !failsAll(e, derivedSignals) {
			x.errors = append(slices.Clone(x.errors), health.NewErroredMeasurement(e.Err(), derivedSignals...))
			break
		}
	}
	x.SubXidCodeCounts = subXidCodeCounts(x.Events)
	x.RecoveryActionCounts = recoveryActionCounts(x.Events, x.EntityID())
	x.UnknownXidCounts = x.countsWhere(covered.isUnknown)
	x.InformationalXidCounts = x.countsWhere(covered.isInformational)
	x.coverage = covered
	return x
}

// failsAll reports whether e already fails every one of signals.
func failsAll(e health.ErroredMeasurement, signals []string) bool {
	for _, s := range signals {
		if !slices.Contains(e.FailedSignals(), s) {
			return false
		}
	}
	return true
}
