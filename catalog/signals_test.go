// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/pcie"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// The built-in measurement types must satisfy the signal-name uniqueness
// invariant that signal-subset routing relies on: within a category, no
// two measurement types share a signal name. A new measurement type or a
// renamed signal that violates it fails here rather than silently
// misrouting a check at evaluation time.
func TestBuiltinMeasurementsSignalUniqueness(t *testing.T) {
	if err := catalog.CheckSignalUniqueness(
		cpu.CPUMeasurement{},
		gpu.GPUMeasurement{},
		infiniband.LinkFlapMeasurement{},
		node.NodeMeasurement{},
		nvlink.NVLinkMeasurement{},
		pcie.RDMATopoMeasurement{},
		xid.XidMeasurement{},
	); err != nil {
		t.Errorf("built-in measurements violate signal-name uniqueness: %v", err)
	}
}

// Every built-in check must be feedable by a built-in measurement type.
// A check whose RequiredSignals no measurement carries can never be
// routed a measurement, so it would silently produce no verdict — this
// guards against a check landing without the measurement (or signal
// name) that feeds it.
func TestBuiltinChecksAreCovered(t *testing.T) {
	cat := catalog.New()
	if err := cat.CheckCoverage(
		cpu.CPUMeasurement{},
		gpu.GPUMeasurement{},
		infiniband.LinkFlapMeasurement{},
		node.NodeMeasurement{},
		nvlink.NVLinkMeasurement{},
		pcie.RDMATopoMeasurement{},
		xid.XidMeasurement{},
	); err != nil {
		t.Errorf("built-in check has no measurement provider: %v", err)
	}
}

// CheckCoverage flags a check whose required signals no supplied
// measurement type carries — the silently-unrunnable case.
func TestCheckCoverage_DetectsUncoveredCheck(t *testing.T) {
	cat := catalog.NewEmpty()
	cat.MustRegister(health.CheckDefinition{
		ID:              "orphan_check",
		Category:        health.Node,
		RequiredSignals: []string{"no_such_signal"},
		Thresholds:      map[string]health.ThresholdSpec{},
		Rules: []health.Rule{
			{Expression: "no_such_signal > 0", Severity: health.Critical, Message: "x"},
		},
	})

	err := cat.CheckCoverage(node.NodeMeasurement{})
	if err == nil {
		t.Fatal("expected an uncovered-check error, got nil")
	}
	if !strings.Contains(err.Error(), "orphan_check") {
		t.Errorf("error = %q, want it to name the uncovered check", err.Error())
	}
}

// A check is covered only within its own category: a measurement type
// carrying the required signal names in a different category does not
// count, since routing gates on category first.
func TestCheckCoverage_RequiresMatchingCategory(t *testing.T) {
	cat := catalog.NewEmpty()
	cat.MustRegister(health.CheckDefinition{
		ID:              "node_gpu_count",
		Category:        health.Node,
		RequiredSignals: []string{"gpu_count"},
		Thresholds:      map[string]health.ThresholdSpec{},
		Rules: []health.Rule{
			{Expression: "gpu_count < 1", Severity: health.Critical, Message: "x"},
		},
	})

	// collidingCPUMeasurement carries gpu_count, but in the CPU category —
	// so it does not cover a Node-category check.
	err := cat.CheckCoverage(collidingCPUMeasurement{})
	if err == nil {
		t.Fatal("expected an uncovered-check error: gpu_count provider is in the wrong category")
	}
	if !strings.Contains(err.Error(), "node_gpu_count") {
		t.Errorf("error = %q, want it to name the uncovered check", err.Error())
	}
}

// collidingNodeMeasurement reuses gpu_count, the signal node.NodeMeasurement
// already owns in the Node category.
type collidingNodeMeasurement struct {
	Count int64 `json:"gpu_count"`
}

func (collidingNodeMeasurement) Category() health.Category           { return health.Node }
func (collidingNodeMeasurement) Errors() []health.ErroredMeasurement { return nil }

// Two measurement types in one category that share a signal name are the
// ambiguity that would let a check route to a measurement it was not
// written for; CheckSignalUniqueness must reject them.
func TestCheckSignalUniqueness_DetectsCollision(t *testing.T) {
	err := catalog.CheckSignalUniqueness(node.NodeMeasurement{}, collidingNodeMeasurement{})
	if err == nil {
		t.Fatal("expected a collision error for a shared signal name, got nil")
	}
	if !strings.Contains(err.Error(), "gpu_count") {
		t.Errorf("error = %q, want it to name the colliding signal gpu_count", err.Error())
	}
}

// A signal name reused across different categories is not a collision:
// routing is scoped by category first, so the same name in two categories
// stays unambiguous.
func TestCheckSignalUniqueness_AllowsSameNameDifferentCategory(t *testing.T) {
	if err := catalog.CheckSignalUniqueness(
		node.NodeMeasurement{},
		collidingCPUMeasurement{},
	); err != nil {
		t.Errorf("same signal name in different categories should be allowed, got %v", err)
	}
}

// collidingCPUMeasurement carries gpu_count too, but in the CPU category.
type collidingCPUMeasurement struct {
	Count int64 `json:"gpu_count"`
}

func (collidingCPUMeasurement) Category() health.Category           { return health.CPU }
func (collidingCPUMeasurement) Errors() []health.ErroredMeasurement { return nil }

// The built-in measurement types must also satisfy the contract
// [health.DerivedSignaler] documents. It is enforced here, over the same
// list as signal uniqueness, rather than in whichever package happens to
// implement the interface today — a second implementer would otherwise
// arrive with no enforcement at all.
func TestBuiltinMeasurementsDerivedSignals(t *testing.T) {
	if err := catalog.CheckDerivedSignals(
		cpu.CPUMeasurement{},
		gpu.GPUMeasurement{},
		infiniband.LinkFlapMeasurement{},
		node.NodeMeasurement{},
		nvlink.NVLinkMeasurement{},
		pcie.RDMATopoMeasurement{},
		xid.XidMeasurement{},
	); err != nil {
		t.Errorf("built-in measurements violate the DerivedSignaler contract: %v", err)
	}
}

// shadowingMeasurement derives a signal named after one of its own json
// fields. ToSignals lets the derived value win, so the field's real value
// never reaches a rule — CheckDerivedSignals must reject it.
type shadowingMeasurement struct {
	Count int64 `json:"widget_count"`
}

func (shadowingMeasurement) Category() health.Category           { return health.Node }
func (shadowingMeasurement) Errors() []health.ErroredMeasurement { return nil }
func (shadowingMeasurement) DerivedSignals() map[string]any {
	return map[string]any{"widget_count": int64(0)}
}

func TestCheckDerivedSignals_DetectsShadowedField(t *testing.T) {
	err := catalog.CheckDerivedSignals(shadowingMeasurement{})
	if err == nil {
		t.Fatal("expected an error for a derived signal shadowing a field, got nil")
	}
	if !strings.Contains(err.Error(), "widget_count") {
		t.Errorf("error = %q, want it to name the shadowed signal widget_count", err.Error())
	}
}

// unstableMeasurement advertises its derived signal only once it holds
// data. Routing reads a zero value, so a check requiring widget_states
// would never be handed one of these.
type unstableMeasurement struct {
	Count int64 `json:"widget_count"`
}

func (unstableMeasurement) Category() health.Category           { return health.Node }
func (unstableMeasurement) Errors() []health.ErroredMeasurement { return nil }
func (m unstableMeasurement) DerivedSignals() map[string]any {
	if m.Count == 0 {
		return map[string]any{}
	}
	return map[string]any{"widget_states": map[string]int64{"up": m.Count}}
}

func TestCheckDerivedSignalKeysStable_DetectsDataDependentKey(t *testing.T) {
	err := catalog.CheckDerivedSignalKeysStable(unstableMeasurement{Count: 3})
	if err == nil {
		t.Fatal("expected an error for a key that appears only when populated, got nil")
	}
	if !strings.Contains(err.Error(), "widget_states") {
		t.Errorf("error = %q, want it to name widget_states", err.Error())
	}
}

// The mirror case: a key present on a zero value but dropped once data
// arrives is equally broken, since routing promised a signal the
// evaluated measurement then does not carry.
func TestCheckDerivedSignalKeysStable_DetectsDisappearingKey(t *testing.T) {
	err := catalog.CheckDerivedSignalKeysStable(unstableMeasurement{})
	if err != nil {
		t.Fatalf("a zero value alone cannot show the instability: %v", err)
	}
	if err := catalog.CheckDerivedSignalKeysStable(vanishingMeasurement{Count: 3}); err == nil {
		t.Error("expected an error for a key that disappears once populated, got nil")
	}
}

type vanishingMeasurement struct {
	Count int64 `json:"widget_count"`
}

func (vanishingMeasurement) Category() health.Category           { return health.Node }
func (vanishingMeasurement) Errors() []health.ErroredMeasurement { return nil }
func (m vanishingMeasurement) DerivedSignals() map[string]any {
	if m.Count == 0 {
		return map[string]any{"widget_states": map[string]int64{}}
	}
	return map[string]any{}
}

// A measurement that derives nothing must pass both checks rather than
// being rejected for not implementing an optional interface.
func TestDerivedSignalChecks_IgnoreNonDerivers(t *testing.T) {
	if err := catalog.CheckDerivedSignals(node.NodeMeasurement{}); err != nil {
		t.Errorf("CheckDerivedSignals rejected a non-deriving measurement: %v", err)
	}
	if err := catalog.CheckDerivedSignalKeysStable(node.NodeMeasurement{}); err != nil {
		t.Errorf("CheckDerivedSignalKeysStable rejected a non-deriving measurement: %v", err)
	}
}

// optionOnlyMeasurement tags its field with options but no name, so its
// signal takes the Go field name, as encoding/json does — and a derived
// signal of that name shadows it.
type optionOnlyMeasurement struct {
	Count int64 `json:",omitempty"`
}

func (optionOnlyMeasurement) Category() health.Category           { return health.Node }
func (optionOnlyMeasurement) Errors() []health.ErroredMeasurement { return nil }
func (optionOnlyMeasurement) DerivedSignals() map[string]any {
	return map[string]any{"Count": int64(0)}
}

func TestCheckDerivedSignals_OptionOnlyTagKeepsFieldName(t *testing.T) {
	err := catalog.CheckDerivedSignals(optionOnlyMeasurement{})
	if err == nil {
		t.Fatal("expected a collision on Count, got nil: an option-only tag was keyed as an empty name")
	}
	if !strings.Contains(err.Error(), `"Count"`) {
		t.Errorf("error = %q, want it to name the shadowed signal Count", err.Error())
	}
}

// A pointer to a measurement with value-receiver methods satisfies
// health.Measurement too. The schema checks must treat it as the value
// it points to rather than panic in reflection.
func TestSignalChecks_AcceptPointerMeasurements(t *testing.T) {
	if err := catalog.CheckSignalUniqueness(&node.NodeMeasurement{}); err != nil {
		t.Errorf("CheckSignalUniqueness(pointer) = %v, want nil", err)
	}
	err := catalog.CheckDerivedSignals(&shadowingMeasurement{})
	if err == nil || !strings.Contains(err.Error(), "widget_count") {
		t.Errorf("CheckDerivedSignals(pointer) = %v, want the widget_count collision it reports for the value", err)
	}
}

// A type passed once by value and once by pointer is still one type; it
// must not be reported as carrying the same signals as itself.
func TestCheckSignalUniqueness_ValueAndPointerAreOneType(t *testing.T) {
	if err := catalog.CheckSignalUniqueness(node.NodeMeasurement{}, &node.NodeMeasurement{}); err != nil {
		t.Errorf("value and pointer of one type reported as colliding: %v", err)
	}
}

// TestEvaluateAll_PointerMeasurement covers the public path: evaluating a
// pointer must produce the verdict its value does, not a reflection panic.
func TestEvaluateAll_PointerMeasurement(t *testing.T) {
	cat := catalog.New(catalog.WithArchitecture("GB200"))
	m := node.NodeMeasurement{GPUCount: 3}

	byValue, err := cat.EvaluateAll(m)
	if err != nil {
		t.Fatalf("EvaluateAll(value): %v", err)
	}
	byPointer, err := cat.EvaluateAll(&m)
	if err != nil {
		t.Fatalf("EvaluateAll(pointer): %v", err)
	}
	if byPointer.OverallSeverity != byValue.OverallSeverity {
		t.Errorf("OverallSeverity: pointer %v, value %v", byPointer.OverallSeverity, byValue.OverallSeverity)
	}
	if len(byPointer.CheckResults) != len(byValue.CheckResults) {
		t.Errorf("CheckResults: pointer %d, value %d", len(byPointer.CheckResults), len(byValue.CheckResults))
	}
}
