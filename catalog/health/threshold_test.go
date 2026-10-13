// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"strings"
	"testing"
)

func TestCheckDefinition_ValidateRejectsRequiredFixedThreshold(t *testing.T) {
	cd := CheckDefinition{
		ID: "node_expected_first",
		Thresholds: map[string]ThresholdSpec{
			"expected_first": {
				Value:            int64(0),
				ValueType:        ThresholdInt64,
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

func TestCheckDefinition_ValidatePerArchitectureMustCoverArchitectures(t *testing.T) {
	cd := CheckDefinition{
		ID:            "speed_check",
		Architectures: []string{"GB200", "H100"},
		Thresholds: map[string]ThresholdSpec{
			"expected_speed": {
				Value:           int64(50000),
				PerArchitecture: map[string]any{"GB200": int64(53125)},
				ValueType:       ThresholdInt64,
				Category:        DefaultThreshold,
			},
		},
	}
	err := cd.Validate()
	if err == nil || !strings.Contains(err.Error(), "H100") {
		t.Fatalf("Validate() = %v, want missing H100 named", err)
	}
}

func TestCheckDefinition_ValidateAllowsNilValueWhenPerArchitectureCovers(t *testing.T) {
	cd := CheckDefinition{
		ID:            "power_check",
		Architectures: []string{"GB200", "H100"},
		Thresholds: map[string]ThresholdSpec{
			"expected_limit": {
				PerArchitecture: map[string]any{
					"GB200": int64(1200),
					"H100":  int64(700),
				},
				ValueType: ThresholdInt64,
				Category:  DefaultThreshold,
			},
		},
	}
	if err := cd.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestCheckDefinition_ValidateRequiresValueWithoutArchitectureCoverage(t *testing.T) {
	cd := CheckDefinition{
		ID: "unrestricted_check",
		Thresholds: map[string]ThresholdSpec{
			"expected_count": {ValueType: ThresholdInt64, Category: DefaultThreshold},
		},
	}
	err := cd.Validate()
	if err == nil || !strings.Contains(err.Error(), "Value is required") {
		t.Fatalf("Validate() = %v, want Value required", err)
	}
}

func TestCheckDefinition_ValidateAcceptsRequiredDefaultThreshold(t *testing.T) {
	cd := CheckDefinition{
		ID: "node_expected_first",
		Thresholds: map[string]ThresholdSpec{
			"expected_first": {
				Value:            int64(0),
				ValueType:        ThresholdInt64,
				Category:         DefaultThreshold,
				RequiredOverride: true,
			},
		},
	}

	if err := cd.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// A check naming itself as a dependency would cite itself as its own cause.
func TestValidate_RejectsSelfDependency(t *testing.T) {
	cd := CheckDefinition{
		ID:           "selfie",
		Dependencies: []string{"other", "selfie"},
		Thresholds:   map[string]ThresholdSpec{},
	}
	err := cd.Validate()
	if err == nil {
		t.Fatal("Validate = nil, want an error for a self-dependency")
	}
	if !strings.Contains(err.Error(), "selfie") {
		t.Errorf("error = %q, want it to name the check", err)
	}
}
