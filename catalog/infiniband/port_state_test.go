// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// ibCatalog builds a catalog on an architecture and execution context the
// InfiniBand checks apply to, so a test that does not exercise filtering
// gets an evaluated verdict rather than a skip.
func ibCatalog(opts ...catalog.Option) *catalog.Catalog {
	defaults := []catalog.Option{
		catalog.WithArchitecture("GB200"),
		catalog.WithExecutionContext(health.OperatorInfra),
	}
	return catalog.New(append(defaults, opts...)...)
}

// port builds a fully-observed port measurement at the default healthy rate.
func port(state, physical string) infiniband.PortMeasurement {
	return infiniband.PortMeasurement{
		Device:        "mlx5_0",
		Port:          1,
		State:         state,
		PhysicalState: physical,
		RateGbps:      400,
		LinkLayer:     "InfiniBand",
	}
}

func TestPortStateDefinition(t *testing.T) {
	def := infiniband.PortState()
	if def.ID != "ib_port_state" || def.Name != "InfiniBand Port State" {
		t.Errorf("identity = %q/%q", def.ID, def.Name)
	}
	if def.Category != health.InfiniBand {
		t.Errorf("Category = %v, want InfiniBand", def.Category)
	}
	want := []string{
		infiniband.SignalIBDevice,
		infiniband.SignalIBPort,
		infiniband.SignalIBState,
		infiniband.SignalIBPhysicalState,
		// Required for the message alone: the check applies to every link
		// layer, so it must name the one it actually found.
		infiniband.SignalIBLinkLayer,
	}
	if got := def.RequiredSignals; !slices.Equal(got, want) {
		t.Errorf("RequiredSignals = %v, want %v", got, want)
	}
	if got, want := def.Architectures, []string{"GB200", "GB300", "B200"}; !slices.Equal(got, want) {
		t.Errorf("Architectures = %v, want %v", got, want)
	}
	if len(def.Rules) != 2 {
		t.Fatalf("Rules = %v, want one per state field", def.Rules)
	}
	for _, r := range def.Rules {
		if r.Severity != health.Critical {
			t.Errorf("rule %q severity = %v, want Critical", r.Expression, r.Severity)
		}
	}
}

func TestPortState_Pass(t *testing.T) {
	v, err := ibCatalog().Evaluate("ib_port_state", port("ACTIVE", "LinkUp"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Skipped || v.Severity != health.Pass {
		t.Fatalf("skipped=%v severity=%v, want evaluated PASS", v.Skipped, v.Severity)
	}
	if !v.RecommendedAction.Equal(health.NoAction) {
		t.Errorf("RecommendedAction = %v, want NoAction", v.RecommendedAction)
	}
}

// The logical and physical states fail independently, so a port stuck in
// INIT after training reports only the state it actually violated.
func TestPortState_Critical(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		physical   string
		wantDetail string
	}{
		{"link never trained", "DOWN", "Polling", "physical state Polling"},
		{"trained but not brought up", "INIT", "LinkUp", "state INIT"},
		{"administratively disabled", "DOWN", "Disabled", "physical state Disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ibCatalog().Evaluate("ib_port_state", port(tc.state, tc.physical))
			if err != nil {
				t.Fatal(err)
			}
			if v.Skipped || v.Severity != health.Critical {
				t.Fatalf("skipped=%v severity=%v, want CRITICAL", v.Skipped, v.Severity)
			}
			joined := strings.Join(v.Messages, "\n")
			if !strings.Contains(joined, tc.wantDetail) {
				t.Errorf("messages = %q, want one naming %q", joined, tc.wantDetail)
			}
			if !strings.Contains(joined, "mlx5_0:1") {
				t.Errorf("messages = %q, want the port identified", joined)
			}
			if !v.RecommendedAction.Equal(health.GenericDrain()) {
				t.Errorf("RecommendedAction = %v, want drain", v.RecommendedAction)
			}
		})
	}
}

// This check rates every link layer, so its message must name the one it
// found. Labelling a dead RoCE NIC as InfiniBand would send an operator to
// the wrong fabric.
func TestPortState_NamesTheActualLinkLayer(t *testing.T) {
	m := port("DOWN", "Polling")
	m.LinkLayer = "Ethernet"

	v, err := ibCatalog().Evaluate("ib_port_state", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Fatalf("severity = %v, want CRITICAL: a down port is down on any link layer", v.Severity)
	}
	joined := strings.Join(v.Messages, "\n")
	if !strings.Contains(joined, "(Ethernet)") {
		t.Errorf("messages = %q, want the Ethernet link layer named", joined)
	}
	if strings.Contains(joined, "InfiniBand") {
		t.Errorf("messages = %q, must not call an Ethernet port InfiniBand", joined)
	}
}

// A down RoCE port is CRITICAL by default; a fleet whose Ethernet ports are
// uncabled adds them to excluded_link_layers instead of disabling the check.
// The list excludes rather than includes so a typo checks more ports, never
// fewer: "Infiniband" excludes nothing and the down port still fails.
func TestPortState_ExcludedLinkLayers(t *testing.T) {
	down := port("DOWN", "Polling")
	down.LinkLayer = "Ethernet"
	cat := ibCatalog(catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "ib_port_state", ThresholdName: "excluded_link_layers", Value: []string{"Ethernet"}},
	)))
	v, err := cat.Evaluate("ib_port_state", down)
	if err != nil {
		t.Fatal(err)
	}
	if v.Skipped || v.Severity != health.Pass {
		t.Errorf("down Ethernet port with Ethernet excluded: skipped=%v severity=%v, want PASS", v.Skipped, v.Severity)
	}
	v, err = cat.Evaluate("ib_port_state", port("DOWN", "Polling"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("down InfiniBand port still checked: severity = %v, want CRITICAL", v.Severity)
	}

	typo := ibCatalog(catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "ib_port_state", ThresholdName: "excluded_link_layers", Value: []string{"Infiniband"}},
	)))
	v, err = typo.Evaluate("ib_port_state", port("DOWN", "Polling"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("misspelled exclusion must exclude nothing: severity = %v, want CRITICAL", v.Severity)
	}
}

// A port that is both down and untrained trips both rules, so an operator
// sees the whole picture rather than one symptom.
func TestPortState_BothRulesMatch(t *testing.T) {
	v, err := ibCatalog().Evaluate("ib_port_state", port("DOWN", "Polling"))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Messages) != 2 {
		t.Errorf("Messages = %v, want one per violated rule", v.Messages)
	}
}

// An operator running a fleet in a different steady state can move both
// expected values.
func TestPortState_ThresholdOverride(t *testing.T) {
	cat := ibCatalog(catalog.WithOverrides(catalog.NewOverrideSet(
		catalog.Override{CheckID: "ib_port_state", ThresholdName: "expected_ib_state", Value: "ARMED"},
	)))

	v, err := cat.Evaluate("ib_port_state", port("ARMED", "LinkUp"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Pass {
		t.Errorf("ARMED with expected_ib_state=ARMED: severity = %v, want PASS", v.Severity)
	}

	v, err = cat.Evaluate("ib_port_state", port("ACTIVE", "LinkUp"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Severity != health.Critical {
		t.Errorf("ACTIVE with expected_ib_state=ARMED: severity = %v, want CRITICAL", v.Severity)
	}
}

// Ports that could not be read at all must fail the check, not pass it.
func TestPortState_Errored(t *testing.T) {
	m := infiniband.ErroredPort("", 0, errors.New("sysfs: read /sys/class/infiniband: permission denied"), infiniband.PortMeasurementSignals...)
	v, err := ibCatalog().Evaluate("ib_port_state", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Skipped || v.Severity != health.Critical {
		t.Fatalf("skipped=%v severity=%v, want CRITICAL when sysfs could not be read", v.Skipped, v.Severity)
	}
}

// A port missing only its rate still has both state signals, so this check
// evaluates normally; the failure belongs to a check that reads the rate.
func TestPortState_UnrelatedSignalErrorStillEvaluates(t *testing.T) {
	m := port("ACTIVE", "LinkUp")
	m.AddError(errors.New("rate: permission denied"), infiniband.SignalIBRateGbps)

	v, err := ibCatalog().Evaluate("ib_port_state", m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Skipped || v.Severity != health.Pass {
		t.Fatalf("skipped=%v severity=%v, want PASS despite the unrelated rate failure", v.Skipped, v.Severity)
	}
}

func TestPortState_SkipsOffArchitecture(t *testing.T) {
	cat := catalog.New(
		catalog.WithArchitecture("A100"),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	v, err := cat.Evaluate("ib_port_state", port("DOWN", "Polling"))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Skipped || v.SkipReason != health.ArchitectureUnsupported {
		t.Errorf("skipped=%v reason=%v, want skipped on A100", v.Skipped, v.SkipReason)
	}
}
