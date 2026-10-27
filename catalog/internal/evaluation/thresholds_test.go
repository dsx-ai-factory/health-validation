// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package evaluation

import (
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

func TestResolveThresholds_NoOverrides(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"max_temp": {Value: 85.0, ValueType: health.ThresholdFloat64, Category: health.DefaultThreshold},
	}

	resolved, err := resolveThresholds("test_check", specs, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	rt := resolved["max_temp"]
	if rt.Value != 85.0 {
		t.Errorf("value = %v, want 85.0", rt.Value)
	}
	if rt.Overridden {
		t.Error("expected not overridden")
	}
}

func TestResolveThresholds_DefaultOverride(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"max_temp": {Value: 85.0, ValueType: health.ThresholdFloat64, Category: health.DefaultThreshold},
	}
	overrides := map[string]any{
		"max_temp": 90.0,
	}

	resolved, err := resolveThresholds("test_check", specs, overrides, "")
	if err != nil {
		t.Fatal(err)
	}

	rt := resolved["max_temp"]
	if rt.Value != 90.0 {
		t.Errorf("value = %v, want 90.0", rt.Value)
	}
	if !rt.Overridden {
		t.Error("expected overridden")
	}
	if rt.OriginalValue != 85.0 {
		t.Errorf("original = %v, want 85.0", rt.OriginalValue)
	}
}

func TestResolveThresholds_FixedRejectsOverride(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"hard_limit": {Value: 100, ValueType: health.ThresholdInt64, Category: health.FixedThreshold},
	}
	overrides := map[string]any{
		"hard_limit": 200,
	}

	_, err := resolveThresholds("test_check", specs, overrides, "")
	if err == nil {
		t.Fatal("expected error for fixed threshold override")
	}

	var fte *health.FixedThresholdError
	if !errors.As(err, &fte) {
		t.Fatalf("expected health.FixedThresholdError, got %T", err)
	}
	if fte.ThresholdName != "hard_limit" {
		t.Errorf("threshold name = %q, want %q", fte.ThresholdName, "hard_limit")
	}
	if fte.CheckID != "test_check" {
		t.Errorf("check ID = %q, want %q", fte.CheckID, "test_check")
	}
}

func TestResolveThresholds_RejectsInvalidOverride(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"expected_pstate": {
			Value:     int64(0),
			ValueType: health.ThresholdInt64,
			ValueMin:  int64(0),
			ValueMax:  int64(15),
			Category:  health.DefaultThreshold,
		},
	}

	tests := []struct {
		override any
		name     string
		wantErr  string
	}{
		{name: "wrong type", override: "P0", wantErr: "expected an integer, got string"},
		{name: "above maximum", override: int64(16), wantErr: "above the maximum 15"},
		{name: "below minimum", override: int64(-1), wantErr: "below the minimum 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveThresholds("test_check", specs,
				map[string]any{"expected_pstate": tt.override}, "")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}

			var tve *health.ThresholdValueError
			if !errors.As(err, &tve) {
				t.Fatalf("expected health.ThresholdValueError, got %T", err)
			}
			if tve.ThresholdName != "expected_pstate" || tve.CheckID != "test_check" {
				t.Errorf("error = %+v, want it to name the threshold and check", tve)
			}
		})
	}
}

// An override is coerced like a catalog value, so YAML's int lands on the
// declared type rather than reaching CEL as an int.
func TestResolveThresholds_NormalizesOverride(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"min_power_w": {Value: float64(65), ValueType: health.ThresholdFloat64, Category: health.DefaultThreshold},
	}

	resolved, err := resolveThresholds("test_check", specs,
		map[string]any{"min_power_w": 70}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved["min_power_w"].Value; got != float64(70) {
		t.Errorf("value = %#v, want float64(70)", got)
	}
}

func TestResolveThresholds_AdjustableOverride(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"warn_temp": {Value: 75.0, ValueType: health.ThresholdFloat64, ValueMin: 0.0, Category: health.AdjustableThreshold},
	}
	overrides := map[string]any{
		"warn_temp": 80.0,
	}

	resolved, err := resolveThresholds("test_check", specs, overrides, "")
	if err != nil {
		t.Fatal(err)
	}

	rt := resolved["warn_temp"]
	if rt.Value != 80.0 {
		t.Errorf("value = %v, want 80.0", rt.Value)
	}
	if !rt.Overridden {
		t.Error("expected overridden")
	}
}

func TestResolveThresholds_PerArchitecture(t *testing.T) {
	specs := map[string]health.ThresholdSpec{
		"expected_speed": {
			Value:           int64(50000),
			PerArchitecture: map[string]any{"H100": int64(26562)},
			ValueType:       health.ThresholdInt64,
			Category:        health.DefaultThreshold,
		},
	}

	// Matching architecture takes the per-arch value.
	resolved, err := resolveThresholds("test_check", specs, nil, "H100")
	if err != nil {
		t.Fatal(err)
	}
	if rt := resolved["expected_speed"]; rt.Value != int64(26562) || rt.OriginalValue != int64(26562) {
		t.Errorf("H100 value = %v (orig %v), want 26562", rt.Value, rt.OriginalValue)
	}

	// An architecture absent from the map falls back to Value.
	resolved, err = resolveThresholds("test_check", specs, nil, "GB200")
	if err != nil {
		t.Fatal(err)
	}
	if rt := resolved["expected_speed"]; rt.Value != int64(50000) {
		t.Errorf("GB200 value = %v, want 50000 (fallback to Value)", rt.Value)
	}

	// An operator override wins over the per-arch value.
	resolved, err = resolveThresholds("test_check", specs,
		map[string]any{"expected_speed": int64(99999)}, "H100")
	if err != nil {
		t.Fatal(err)
	}
	if rt := resolved["expected_speed"]; rt.Value != int64(99999) || !rt.Overridden {
		t.Errorf("override value = %v overridden=%v, want 99999 overridden", rt.Value, rt.Overridden)
	}
}

// A Fixed threshold that also requires an override describes a check that can
// never run: unconfigured it skips, configured it errors.
