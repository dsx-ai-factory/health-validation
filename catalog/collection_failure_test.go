// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// probeGPU builds a gpu_pstate_probe input. A nonzero pstate makes the rule
// match, so a CRITICAL verdict that carries it proves the rules ran.
func probeGPU(id string, key health.EntityKey, pstate int64, failed ...string) gpu.GPUMeasurement {
	m := gpu.GPUMeasurement{GPUID: id, GPUPstate: pstate}
	for _, signal := range failed {
		m.AddError(errors.New(signal+" read failed"), signal)
	}
	if key != (health.EntityKey{}) {
		m.SetEntityKey(key)
	}
	return m
}

func lostGPU(id string, key health.EntityKey, err error) gpu.GPUMeasurement {
	m := gpu.ErroredGPU(id, err)
	if key != (health.EntityKey{}) {
		m.SetEntityKey(key)
	}
	return m
}

// Values under an identity nothing verifies are rejected, while a failure on a
// required signal is reported under any identity without running the rules.
func TestEvaluate_CollectionFailureAcrossIdentities(t *testing.T) {
	const pstate, power = gpu.SignalGPUPstate, gpu.SignalAveragePowerDrawW
	var (
		none     health.EntityKey
		slot     = health.ProvisionalEntity("nvml", "0")
		source   = health.SourceEntity("nvml")
		check    = health.Drain(health.RemediationActionCheckCollection)
		retry    = health.Drain(health.RemediationActionRetryCollection)
		lost     = errors.New("handle unavailable")
		timedOut = fmt.Errorf("gpu sensor: %w", health.ErrCollectionTimeout)
	)
	// An empty reason expects a verdict: rules ran when ruled, a collection
	// failure with action otherwise.
	tests := []struct {
		name     string
		reason   string
		entities []string
		action   health.RecommendedAction
		m        gpu.GPUMeasurement
		ruled    bool
	}{
		{name: "verified values", m: probeGPU("GPU-A", none, 8), entities: []string{"GPU-A"}, ruled: true},
		{name: "verified complete failure", m: lostGPU("GPU-A", none, lost), entities: []string{"GPU-A"}, action: check},
		{name: "verified required failed", m: probeGPU("GPU-A", none, 0, pstate), entities: []string{"GPU-A"}, action: check},
		{name: "verified unrelated failed", m: probeGPU("GPU-A", none, 8, power), entities: []string{"GPU-A"}, ruled: true},
		{name: "verified timeout", m: lostGPU("GPU-A", none, timedOut), entities: []string{"GPU-A"}, action: retry},
		{name: "unidentified values", m: probeGPU("", none, 8), reason: "name no entity"},
		{name: "unidentified complete failure", m: lostGPU("", none, lost), action: check},
		{name: "unidentified required failed", m: probeGPU("", none, 0, pstate), action: check},
		{name: "unidentified unrelated failed", m: probeGPU("", none, 8, power), reason: "name no entity"},
		{name: "provisional values", m: probeGPU("nvml-index-0", slot, 8), reason: "verified ID"},
		{name: "provisional complete failure", m: slotFailure("nvml", "0"), entities: []string{"nvml-index-0"}, action: check},
		{name: "provisional required failed", m: probeGPU("nvml-index-0", slot, 0, pstate), entities: []string{"nvml-index-0"}, action: check},
		{name: "provisional unrelated failed", m: probeGPU("nvml-index-0", slot, 8, power), reason: "verified ID"},
		{name: "source values", m: probeGPU("nvml", source, 8), reason: "verified ID"},
		{name: "source complete failure", m: lostGPU("nvml", source, lost), entities: []string{"nvml"}, action: check},
		{name: "source required failed", m: probeGPU("nvml", source, 0, pstate), entities: []string{"nvml"}, action: check},
		{name: "source unrelated failed", m: probeGPU("nvml", source, 8, power), reason: "verified ID"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/Evaluate", func(t *testing.T) {
			v, err := probeCatalog().Evaluate("gpu_pstate_probe", tt.m)
			if tt.reason != "" {
				wantAmbiguousReason(t, err, tt.reason)
				return
			}
			wantVerdict(t, v, err, tt.ruled, tt.action, tt.entities)
		})
		t.Run(tt.name+"/EvaluateAll", func(t *testing.T) {
			nv, err := probeCatalog().EvaluateAll(tt.m)
			if tt.reason != "" {
				wantAmbiguousReason(t, err, tt.reason)
				if len(nv.CheckResults) != 0 || len(nv.SkippedChecks) != 0 {
					t.Errorf("ambiguous run produced a partial verdict: %+v", nv)
				}
				return
			}
			var probe []health.CheckVerdict
			for _, v := range nv.CheckResults {
				if v.CheckID == "gpu_pstate_probe" {
					probe = append(probe, v)
				}
			}
			if len(probe) != 1 {
				t.Fatalf("gpu_pstate_probe results = %+v, want one (err %v)", probe, err)
			}
			wantVerdict(t, probe[0], err, tt.ruled, tt.action, tt.entities)
		})
	}
}

func wantAmbiguousReason(t *testing.T, err error, reason string) {
	t.Helper()
	var ambiguous *health.AmbiguousMeasurementError
	if !errors.Is(err, health.ErrAmbiguousMeasurement) || !errors.As(err, &ambiguous) {
		t.Fatalf("err = %v, want an AmbiguousMeasurementError", err)
	}
	if ambiguous.CheckID != "gpu_pstate_probe" || !strings.Contains(ambiguous.Reason, reason) {
		t.Errorf("ambiguity = %q %q, want gpu_pstate_probe and a reason containing %q", ambiguous.CheckID, ambiguous.Reason, reason)
	}
}

func wantVerdict(t *testing.T, v health.CheckVerdict, err error, ruled bool, action health.RecommendedAction, entities []string) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v, want a verdict", err)
	}
	want := health.Unknown
	if ruled {
		want = health.Critical
	}
	if v.Severity != want {
		t.Errorf("severity = %v, want %v", v.Severity, want)
	}
	if !slices.Equal(v.AffectedEntities, entities) {
		t.Errorf("affected entities = %v, want %v", v.AffectedEntities, entities)
	}
	if ruled {
		if !slices.Equal(v.Messages, []string{"wrong pstate"}) || v.ObservedValues[gpu.SignalGPUPstate] != int64(8) || !v.RecommendedAction.IsZero() {
			t.Errorf("verdict = %+v, want the rule's message, pstate 8 observed and no action", v)
		}
		return
	}
	if len(v.Messages) != 1 || !strings.HasPrefix(v.Messages[0], "measurement unavailable: ") {
		t.Errorf("messages = %q, want one collection-failure message", v.Messages)
	}
	if v.ObservedValues != nil {
		t.Errorf("observed values = %v, want none: the rules did not run", v.ObservedValues)
	}
	if !v.RecommendedAction.Equal(action) || v.ConditionScope != health.NodeScope {
		t.Errorf("action %v scope %v, want %v at NODE", v.RecommendedAction, v.ConditionScope, action)
	}
}
