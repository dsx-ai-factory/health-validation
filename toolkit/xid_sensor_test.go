// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package toolkit_test

import (
	"context"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/catalogtest"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// xidPresenceCheck is a throwaway check used only to prove the
// sensor → catalog data flow. It is not a production check; it fires
// CRITICAL whenever any Xid event was observed.
func xidPresenceCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:                "xid_present_test",
		Name:              "Xid Present (test)",
		Description:       "test-only: any Xid event is critical",
		Category:          health.Xid,
		RequiredSignals:   []string{xid.SignalEventCount},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Rules: []health.Rule{
			{
				Expression: "event_count > 0",
				Severity:   health.Critical,
				Message:    "observed ${event_count} Xid event(s)",
			},
		},
	}
}

func xidCatalog() *catalog.Catalog {
	cat := catalog.NewEmpty(
		catalog.WithArchitecture(catalogtest.DummyArchitecture),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	cat.MustRegister(xidPresenceCheck())
	return cat
}

func TestXidSensor_Name(t *testing.T) {
	s := toolkit.NewXidSensor(toolkit.MockXid())
	if s.Name() != "xid" {
		t.Errorf("Name() = %q, want xid", s.Name())
	}
}

func TestXidSensor_Measure_AggregatesEvents(t *testing.T) {
	s := toolkit.NewXidSensor(toolkit.MockXid(
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1, name=a",
		"NVRM: Xid (PCI:0000:02:00): 79, pid=2, name=b",
	))

	ms, err := s.Measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d measurements, want 1 aggregate", len(ms))
	}
	m, ok := ms[0].(xid.XidMeasurement)
	if !ok {
		t.Fatalf("measurement is %T, want xid.XidMeasurement", ms[0])
	}
	if m.EventCount != 2 {
		t.Errorf("EventCount = %d, want 2", m.EventCount)
	}
	if got := m.XidCounts[79]; got != 2 {
		t.Errorf("XidCounts[79] = %d, want 2", got)
	}
}

// TestXidSensor_InjectedFailure simulates Xid faults in a fake syslog
// and verifies the catalog escalates to CRITICAL — the core use case.
func TestXidSensor_InjectedFailure(t *testing.T) {
	s := toolkit.NewXidSensor(toolkit.MockXid(
		"NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3",
	))

	nv, err := toolkit.Collect(xidCatalog(), s)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Critical {
		t.Errorf("OverallSeverity = %v, want CRITICAL", nv.OverallSeverity)
	}
}

// TestXidSensor_CleanBoot verifies that with no Xid events the
// aggregate measurement is still emitted and the check passes.
func TestXidSensor_CleanBoot(t *testing.T) {
	s := toolkit.NewXidSensor(toolkit.MockXid())

	nv, err := toolkit.Collect(xidCatalog(), s)
	if err != nil {
		t.Fatal(err)
	}
	if nv.OverallSeverity != health.Pass {
		t.Errorf("OverallSeverity = %v, want PASS", nv.OverallSeverity)
	}
	if len(nv.CheckResults) != 1 {
		t.Errorf("CheckResults len = %d, want 1 (check ran, not skipped)", len(nv.CheckResults))
	}
}
