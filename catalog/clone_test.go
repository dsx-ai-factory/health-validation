// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"errors"
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
	"github.com/dsx-ai-factory/health-validation/catalog/node"
	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

func clonableCheck() health.CheckDefinition {
	return health.CheckDefinition{
		ID:              "clone_check",
		Name:            "Clone",
		RequiredSignals: []string{"a", "b"},
		Thresholds: map[string]health.ThresholdSpec{
			"limit": {Value: 1, PerArchitecture: map[string]any{"H100": 2}, Category: health.DefaultThreshold},
		},
		Rules: []health.Rule{{
			Expression:        "true",
			RecommendedAction: health.Drain(health.RemediationActionRestartOS),
		}},
	}
}

func mutateDefinition(cd *health.CheckDefinition) {
	cd.RequiredSignals[0] = "mutated"
	cd.Thresholds["limit"].PerArchitecture["H100"] = 99
	cd.Thresholds["added"] = health.ThresholdSpec{}
	cd.Rules[0].RecommendedAction.RemediationActions[0] = health.RemediationActionServiceRequired
}

func assertDefinitionPristine(t *testing.T, cd *health.CheckDefinition) {
	t.Helper()
	if cd.RequiredSignals[0] != "a" {
		t.Errorf("RequiredSignals[0] = %q, want %q", cd.RequiredSignals[0], "a")
	}
	if got := cd.Thresholds["limit"].PerArchitecture["H100"]; got != 2 {
		t.Errorf("PerArchitecture[H100] = %v, want 2", got)
	}
	if _, ok := cd.Thresholds["added"]; ok {
		t.Error("Thresholds gained an entry from a caller mutation")
	}
	if got := cd.Rules[0].RecommendedAction.RemediationActions[0]; got != health.RemediationActionRestartOS {
		t.Errorf("rule remediation action = %v, want %v", got, health.RemediationActionRestartOS)
	}
}

func TestCatalog_RegisterCopiesDefinition(t *testing.T) {
	cat := NewEmpty()
	cd := clonableCheck()
	if err := cat.Register(cd); err != nil {
		t.Fatal(err)
	}
	mutateDefinition(&cd)

	got, err := cat.Get("clone_check")
	if err != nil {
		t.Fatal(err)
	}
	assertDefinitionPristine(t, got)
}

func TestCatalog_GetReturnsCopy(t *testing.T) {
	cat := NewEmpty()
	if err := cat.Register(clonableCheck()); err != nil {
		t.Fatal(err)
	}
	first, err := cat.Get("clone_check")
	if err != nil {
		t.Fatal(err)
	}
	mutateDefinition(first)

	second, err := cat.Get("clone_check")
	if err != nil {
		t.Fatal(err)
	}
	assertDefinitionPristine(t, second)
}

func TestCatalog_ListReturnsCopies(t *testing.T) {
	cat := NewEmpty()
	if err := cat.Register(clonableCheck()); err != nil {
		t.Fatal(err)
	}
	listed := cat.List(Filter{})
	if len(listed) != 1 {
		t.Fatalf("List returned %d definitions, want 1", len(listed))
	}
	mutateDefinition(listed[0])

	got, err := cat.Get("clone_check")
	if err != nil {
		t.Fatal(err)
	}
	assertDefinitionPristine(t, got)
}

func TestOverrideSet_ForCheckReturnsCopy(t *testing.T) {
	set := NewOverrideSet(Override{CheckID: "c", ThresholdName: "t", Value: 1})
	set.ForCheck("c")["t"] = 2
	set.ForCheck("c")["extra"] = 3

	got := set.ForCheck("c")
	if len(got) != 1 || got["t"] != 1 {
		t.Errorf("ForCheck = %v, want map[t:1]", got)
	}
}

func TestErroredHelpersDefaultToEveryRegisteredSignal(t *testing.T) {
	err := errors.New("unobservable")
	for name, tc := range map[string]struct {
		got  []health.ErroredMeasurement
		want []string
	}{
		"cpu":        {cpu.ErroredCPU(err).Errors(), cpu.MeasurementSignals()},
		"gpu":        {gpu.ErroredGPU("g", err).Errors(), gpu.MeasurementSignals()},
		"node":       {node.ErroredNode(err).Errors(), node.MeasurementSignals()},
		"nvlink":     {nvlink.ErroredNVLink("g", err).Errors(), nvlink.MeasurementSignals()},
		"xid":        {xid.ErroredXid("boot", err).Errors(), xid.MeasurementSignals()},
		"infiniband": {infiniband.ErroredLinkFlap("boot", err).Errors(), infiniband.MeasurementSignals()},
	} {
		if len(tc.got) != 1 || !slices.Equal(tc.got[0].FailedSignals(), tc.want) {
			t.Errorf("%s: FailedSignals = %v, want %v", name, tc.got, tc.want)
		}
	}
}

func TestMeasurementSignalsReturnsCopy(t *testing.T) {
	first := gpu.MeasurementSignals()
	want := slices.Clone(first)
	first[0] = "mutated"
	if got := gpu.MeasurementSignals(); !slices.Equal(got, want) {
		t.Errorf("MeasurementSignals() = %v, want %v", got, want)
	}
}

func TestErroredMeasurementCopiesSignals(t *testing.T) {
	signals := []string{"x", "y"}
	em := health.NewErroredMeasurement(errors.New("boom"), signals...)
	signals[0] = "mutated"
	em.FailedSignals()[1] = "mutated"

	if got := em.FailedSignals(); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("FailedSignals = %v, want [x y]", got)
	}
}
