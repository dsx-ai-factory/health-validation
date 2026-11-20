// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package evaluation

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestEvaluateRejectsUndeclaredCELInput(t *testing.T) {
	const expression = "gpu_pstate != 0"
	cd := &health.CheckDefinition{
		ID:                "gpu_pstate_probe",
		Category:          health.GPU,
		RequiredSignals:   []string{gpu.SignalGPUID},
		ExecutionContexts: []health.ExecutionContext{health.OperatorInfra},
		Rules:             []health.Rule{{Expression: expression, Severity: health.Critical}},
	}
	p := Params{Architecture: "GB200", ExecutionContext: health.OperatorInfra}
	engine := NewEvaluationEngine()

	wantUndeclared := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected an undeclared-reference error")
		}
		for _, want := range []string{cd.ID, expression, "undeclared reference to 'gpu_pstate'"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to contain %q", err, want)
			}
		}
	}

	for _, test := range []struct {
		name       string
		pstateRead bool
	}{
		{name: "failed pstate read"},
		{name: "successful pstate read", pstateRead: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := gpu.GPUMeasurement{GPUID: "GPU-0"}
			if test.pstateRead {
				m.GPUPstate = 8
			} else {
				m.AddError(errors.New("pstate unavailable"), gpu.SignalGPUPstate)
			}
			t.Run("single", func(t *testing.T) {
				v, err := engine.EvaluateCheck(cd, m, health.Host{}, p)
				wantUndeclared(t, err)
				if !reflect.DeepEqual(v, health.CheckVerdict{}) {
					t.Errorf("verdict = %+v, want an empty verdict", v)
				}
			})
			t.Run("batch", func(t *testing.T) {
				checks := map[string]*health.CheckDefinition{cd.ID: cd}
				results, err := engine.EvaluateBatch(checks, []health.Measurement{m}, health.Host{}, func(string) Params { return p })
				wantUndeclared(t, err)
				if results != nil {
					t.Errorf("results = %+v, want none", results)
				}
			})
		})
	}
}

func TestEvaluateUsesDeclaredInputsForRulesAndMessages(t *testing.T) {
	cd := &health.CheckDefinition{
		ID:              "gpu_pstate_probe",
		Category:        health.GPU,
		RequiredSignals: []string{gpu.SignalGPUID, gpu.SignalGPUPstate, gpu.SignalPowerLimitW},
		Thresholds: map[string]health.ThresholdSpec{
			"expected_pstate": {Value: int64(9), ValueType: health.ThresholdInt64, Category: health.DefaultThreshold},
		},
		Rules: []health.Rule{{
			Expression: "gpu_id == 'GPU-0' && gpu_pstate > expected_pstate",
			Message:    "GPU ${gpu_id} at ${gpu_pstate} exceeds ${expected_pstate}",
			Severity:   health.Warning,
		}},
	}
	m := gpu.GPUMeasurement{GPUID: "GPU-0", GPUPstate: 8, PowerLimitW: 300, ComputeProcessCount: 2}
	p := Params{Architecture: "GB200", Overrides: map[string]any{"expected_pstate": int64(5)}}
	v, err := NewEvaluationEngine().EvaluateCheck(cd, m, health.Host{}, p)
	if err != nil {
		t.Fatalf("EvaluateCheck: %v", err)
	}
	if v.Severity != health.Warning {
		t.Errorf("severity = %v, want WARNING", v.Severity)
	}
	wantMessage := "GPU GPU-0 at 8 exceeds 5"
	if !slices.Equal(v.Messages, []string{wantMessage}) {
		t.Errorf("messages = %q, want [%q]", v.Messages, wantMessage)
	}
	wantObserved := map[string]any{gpu.SignalGPUID: "GPU-0", gpu.SignalGPUPstate: int64(8), gpu.SignalPowerLimitW: float64(300)}
	if !reflect.DeepEqual(v.ObservedValues, wantObserved) {
		t.Errorf("observed values = %v, want %v", v.ObservedValues, wantObserved)
	}
	if got := v.Thresholds["expected_pstate"].Value; got != int64(5) {
		t.Errorf("resolved pstate threshold = %v, want 5", got)
	}
}
