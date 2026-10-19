// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"errors"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// linkEvt149_4 is one Xid 149 NETIR_LINK_EVT event of fault subtype 149.4.
func linkEvt149_4() xid.XidMeasurement {
	return xid.XidMeasurement{
		XidCounts:      map[int64]int64{149: 1},
		FatalXidCounts: map[int64]int64{149: 1},
		EventCount:     1,
		Events: []xid.XidEvent{{
			PCIAddress: "0019:01:00",
			XidCode:    149,
			NVLink5:    &xid.NVLink5Detail{SubComponent: "NETIR_LINK_EVT", IntrInfo: "0x004505c6", Fatal: true},
		}},
	}
}

// derivedFrom runs the accumulator over measurements and returns the one
// XidDerivedMeasurement it must produce.
func derivedFrom(t *testing.T, measurements ...health.Measurement) xid.XidDerivedMeasurement {
	t.Helper()
	out, err := xid.Accumulator{}.Accumulate(measurements)
	if err != nil {
		t.Fatalf("Accumulate: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Accumulate returned %d measurements, want 1", len(out))
	}
	d, ok := out[0].(xid.XidDerivedMeasurement)
	if !ok {
		t.Fatalf("Accumulate returned %T, want XidDerivedMeasurement", out[0])
	}
	return d
}

// A sensor supplies only events; the stock catalog derives the subtype
// counts and the per-subcode check reads them.
func TestAccumulator_SubXidCodeReachesItsCheck(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	nv, err := cat.EvaluateAll(linkEvt149_4())
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	v, ok := checkResult(nv, "gpu_xid_149_4")
	if !ok {
		t.Fatal("gpu_xid_149_4 verdict not found")
	}
	if v.Severity != health.Critical {
		t.Errorf("gpu_xid_149_4 severity = %v, want CRITICAL", v.Severity)
	}
}

// An unreadable journal must not read as "no fault": the derived signals
// are reported failed, so the checks reading them fail rather than pass.
func TestAccumulator_CarriesUnreadableJournalForward(t *testing.T) {
	d := derivedFrom(t, xid.ErroredXid("boot", errors.New("journal unreadable"), xid.MeasurementSignals()...))
	if len(d.Errors()) == 0 {
		t.Fatal("derived measurement carries no error, want the journal failure carried forward")
	}

	cat := catalog.New(catalog.WithArchitecture("GB200"), catalog.WithExecutionContext(health.OperatorInfra))
	nv, err := cat.EvaluateAll(xid.ErroredXid("boot", errors.New("journal unreadable"), xid.MeasurementSignals()...))
	if err != nil {
		t.Fatalf("EvaluateAll: %v", err)
	}
	if v, ok := checkResult(nv, "gpu_xid_149_4"); !ok || v.Severity == health.Pass {
		t.Errorf("gpu_xid_149_4 = %+v (found %v), want a failed verdict rather than PASS", v.Severity, ok)
	}
}

// The driver version arrives on the node measurement. Whether it was
// collected, unreadable, or absent, the derivation runs: an unknown version
// narrows version-dependent decoding but is never a reason to withhold the
// derived signals.
func TestAccumulator_DriverVersionIsOptionalInput(t *testing.T) {
	for name, extra := range map[string][]health.Measurement{
		"collected":  {node.NodeMeasurement{DriverVersion: "575.51.03"}},
		"unreadable": {node.ErroredNode(errors.New("SystemGetDriverVersion failed"), node.SignalDriverVersion)},
		"absent":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			d := derivedFrom(t, append([]health.Measurement{linkEvt149_4()}, extra...)...)
			if len(d.Errors()) != 0 || d.SubXidCodeCounts["149.4"] != 1 {
				t.Errorf("derived = %+v (errors %v), want 149.4 -> 1 and no errors", d.SubXidCodeCounts, d.Errors())
			}
		})
	}
}

// Checks and their accumulator register separately. A catalog given the
// checks without the accumulator never routes the per-subcode checks a
// measurement, and CheckCoverage is what reports it.
func TestCheckCoverage_FlagsMissingAccumulator(t *testing.T) {
	cat := catalog.NewEmpty()
	for _, cd := range xid.Checks() {
		cat.MustRegister(cd)
	}
	if err := cat.CheckCoverage(xid.XidMeasurement{}); err == nil {
		t.Error("CheckCoverage = nil, want the per-subcode checks reported uncovered without the accumulator")
	}

	if err := cat.RegisterAccumulator(xid.Accumulator{}); err != nil {
		t.Fatal(err)
	}
	if err := cat.CheckCoverage(xid.XidMeasurement{}); err != nil {
		t.Errorf("CheckCoverage with the accumulator registered = %v, want nil", err)
	}
}
