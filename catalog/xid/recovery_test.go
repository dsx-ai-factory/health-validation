// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid_test

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func recoveryEvent(device, code string, sequence int64) xid.XidEvent {
	return xid.XidEvent{
		PCIAddress: device, XidCode: 154, Sequence: sequence,
		RecoveryAction: &xid.RecoveryAction{FromCode: "0x0", FromName: "None", ToCode: code, ToName: "printed name"},
	}
}

func recoveryMeasurement(events ...xid.XidEvent) xid.XidMeasurement {
	m := xid.XidMeasurement{Since: "boot", Events: events, EventCount: int64(len(events)), XidCounts: map[int64]int64{}}
	for _, event := range events {
		m.XidCounts[event.XidCode]++
	}
	return m
}

func TestXid154Recovery(t *testing.T) {
	for _, tc := range []struct {
		code     string
		action   health.RecommendedAction
		severity health.Severity
	}{
		{"0x0", health.NoAction, health.Info},
		{"0x1", health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired), health.Critical},
		{"0x2", health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"0x3", health.Drain(health.RemediationActionXid154), health.Warning},
		{"0x4", health.Drain(health.RemediationActionResetGPU, health.RemediationActionServiceRequired), health.Critical},
		{"0x5", health.Monitor, health.Warning},
		{"0x6", health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"0x7", health.Drain(health.RemediationActionRestartOS), health.Critical},
		{"0xff", health.Monitor, health.Warning},
		{"garbled", health.Monitor, health.Warning},
	} {
		t.Run(tc.code, func(t *testing.T) {
			m := recoveryMeasurement(recoveryEvent("a", tc.code, 1))
			v, err := xidCatalog(t).Evaluate("gpu_xid_154", m)
			if err != nil {
				t.Fatal(err)
			}
			if err := catalog.CheckDerivedSignalKeysStable(m); err != nil {
				t.Fatal(err)
			}
			if v.Severity != tc.severity || !v.RecommendedAction.Equal(tc.action) {
				t.Fatalf("verdict = %+v", v)
			}
		})
	}
}

func TestXid154Chronology(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []xid.XidEvent
		want   health.Severity
	}{
		{"None retires only own recovery", []xid.XidEvent{recoveryEvent("a", "0x2", 1), recoveryEvent("a", "0x0", 2)}, health.Info},
		{"shuffled events use explicit sequence", []xid.XidEvent{recoveryEvent("a", "0x0", 2), recoveryEvent("a", "0x2", 1)}, health.Info},
		{"other device remains", []xid.XidEvent{recoveryEvent("a", "0x2", 1), recoveryEvent("b", "0x0", 2)}, health.Critical},
		{"missing sequence cannot erase", []xid.XidEvent{recoveryEvent("a", "0x2", 0), recoveryEvent("a", "0x0", 0)}, health.Critical},
		{"tied sequence cannot erase", []xid.XidEvent{recoveryEvent("a", "0x2", 1), recoveryEvent("a", "0x0", 1)}, health.Critical},
		{"unknown device cannot erase", []xid.XidEvent{recoveryEvent("", "0x2", 1), recoveryEvent("", "0x0", 2)}, health.Critical},
		{"bare report remains visible", []xid.XidEvent{{XidCode: 154, PCIAddress: "a", Sequence: 1}}, health.Warning},
		{"incomplete transition remains visible", []xid.XidEvent{{XidCode: 154, PCIAddress: "a", RecoveryAction: &xid.RecoveryAction{ToCode: "0x2"}}}, health.Warning},
		{"later bare report cannot retire request", []xid.XidEvent{recoveryEvent("a", "0x1", 1), {XidCode: 154, PCIAddress: "a", Sequence: 2}}, health.Critical},
		{"later malformed report cannot retire request", []xid.XidEvent{recoveryEvent("a", "0x2", 1), recoveryEvent("a", "garbled", 2)}, health.Critical},
		{"bare report after None remains visible", []xid.XidEvent{recoveryEvent("a", "0x2", 1), recoveryEvent("a", "0x0", 2), {XidCode: 154, PCIAddress: "a", Sequence: 3}}, health.Warning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := xidCatalog(t).Evaluate("gpu_xid_154", recoveryMeasurement(tc.events...))
			if err != nil {
				t.Fatal(err)
			}
			if v.Severity != tc.want {
				t.Fatalf("verdict = %+v, want %v", v, tc.want)
			}
		})
	}
}

func TestXid154ThresholdAndOriginatingFault(t *testing.T) {
	cat := xidCatalog(t, occurrencesOverride("gpu_xid_154", 2))
	for count := 1; count <= 2; count++ {
		var events []xid.XidEvent
		for i := range count {
			events = append(events, recoveryEvent("a", "0x2", int64(i+1)))
		}
		v, err := cat.Evaluate("gpu_xid_154", recoveryMeasurement(events...))
		if err != nil {
			t.Fatal(err)
		}
		want := health.Pass
		if count == 2 {
			want = health.Critical
		}
		if v.Severity != want {
			t.Fatalf("%d reports: %+v", count, v)
		}
	}
	for _, code := range []string{"0x2", "0x0"} {
		m := recoveryMeasurement(xid.XidEvent{XidCode: 54, PCIAddress: "a"}, recoveryEvent("a", code, 2))
		v, err := xidCatalogFor(t, "H100").EvaluateAll(m)
		if err != nil {
			t.Fatal(err)
		}
		if v.OverallSeverity != health.Critical || !v.RecommendedAction.Equal(health.Drain(health.RemediationActionServiceRequired)) {
			t.Fatalf("originating hardware fault lost: %+v", v)
		}
	}
}

func TestXid154MalformedNumericTransition(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"0x100000000", "0x2"},
		{"0x0", "0x100000000"},
		{"invalid", "0x0"},
	} {
		t.Run(tc.from+" to "+tc.to, func(t *testing.T) {
			event := recoveryEvent("a", tc.to, 1)
			event.RecoveryAction.FromCode = tc.from
			v, err := xidCatalog(t).Evaluate("gpu_xid_154", recoveryMeasurement(event))
			if err != nil {
				t.Fatal(err)
			}
			if v.Severity != health.Warning || !v.RecommendedAction.Equal(health.Monitor) {
				t.Fatalf("malformed transition must remain visible without a guessed repair: %+v", v)
			}
		})
	}
}
