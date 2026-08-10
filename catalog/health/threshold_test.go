package health

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveThresholds_NoOverrides(t *testing.T) {
	specs := map[string]ThresholdSpec{
		"max_temp": {Value: 85.0, Category: DefaultThreshold},
	}

	resolved, err := ResolveThresholds("test_check", specs, nil, "")
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
	specs := map[string]ThresholdSpec{
		"max_temp": {Value: 85.0, Category: DefaultThreshold},
	}
	overrides := map[string]any{
		"max_temp": 90.0,
	}

	resolved, err := ResolveThresholds("test_check", specs, overrides, "")
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
	specs := map[string]ThresholdSpec{
		"hard_limit": {Value: 100, Category: FixedThreshold},
	}
	overrides := map[string]any{
		"hard_limit": 200,
	}

	_, err := ResolveThresholds("test_check", specs, overrides, "")
	if err == nil {
		t.Fatal("expected error for fixed threshold override")
	}

	var fte *FixedThresholdError
	if !errors.As(err, &fte) {
		t.Fatalf("expected FixedThresholdError, got %T", err)
	}
	if fte.ThresholdName != "hard_limit" {
		t.Errorf("threshold name = %q, want %q", fte.ThresholdName, "hard_limit")
	}
	if fte.CheckID != "test_check" {
		t.Errorf("check ID = %q, want %q", fte.CheckID, "test_check")
	}
}

func TestResolveThresholds_AdjustableOverride(t *testing.T) {
	specs := map[string]ThresholdSpec{
		"warn_temp": {Value: 75.0, Category: AdjustableThreshold},
	}
	overrides := map[string]any{
		"warn_temp": 80.0,
	}

	resolved, err := ResolveThresholds("test_check", specs, overrides, "")
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
	specs := map[string]ThresholdSpec{
		"expected_speed": {
			Value:           int64(50000),
			PerArchitecture: map[string]any{"H100": int64(26562)},
			Category:        DefaultThreshold,
		},
	}

	// Matching architecture takes the per-arch value.
	resolved, err := ResolveThresholds("test_check", specs, nil, "H100")
	if err != nil {
		t.Fatal(err)
	}
	if rt := resolved["expected_speed"]; rt.Value != int64(26562) || rt.OriginalValue != int64(26562) {
		t.Errorf("H100 value = %v (orig %v), want 26562", rt.Value, rt.OriginalValue)
	}

	// An architecture absent from the map falls back to Value.
	resolved, err = ResolveThresholds("test_check", specs, nil, "GB200")
	if err != nil {
		t.Fatal(err)
	}
	if rt := resolved["expected_speed"]; rt.Value != int64(50000) {
		t.Errorf("GB200 value = %v, want 50000 (fallback to Value)", rt.Value)
	}

	// An operator override wins over the per-arch value.
	resolved, err = ResolveThresholds("test_check", specs,
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
func TestCheckDefinition_ValidateRejectsRequiredFixedThreshold(t *testing.T) {
	cd := CheckDefinition{
		ID: "node_expected_first",
		Thresholds: map[string]ThresholdSpec{
			"expected_first": {
				Value:            int64(0),
				Category:         FixedThreshold,
				RequiredOverride: true,
			},
		},
	}

	err := cd.Validate()
	if err == nil || !strings.Contains(err.Error(), "expected_first") {
		t.Fatalf("Validate() = %v, want the contradictory threshold named", err)
	}
}

func TestCheckDefinition_ValidateAcceptsRequiredDefaultThreshold(t *testing.T) {
	cd := CheckDefinition{
		ID: "node_expected_first",
		Thresholds: map[string]ThresholdSpec{
			"expected_first": {
				Value:            int64(0),
				Category:         DefaultThreshold,
				RequiredOverride: true,
			},
		},
	}

	if err := cd.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
