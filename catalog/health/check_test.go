// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"strings"
	"testing"
)

// threshold-value validation, exercised through the public entry point.
func TestCheckDefinition_ValidateThresholdValues(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		archs   []string
		spec    ThresholdSpec
	}{
		{
			name:    "missing value type",
			spec:    ThresholdSpec{Value: int64(4), Category: DefaultThreshold},
			wantErr: "ValueType is required",
		},
		{
			name: "bounds on an unordered type",
			spec: ThresholdSpec{
				Value:     "P0",
				ValueType: ThresholdString,
				ValueMin:  "A",
				Category:  DefaultThreshold,
			},
			wantErr: "require an ordered value type",
		},
		{
			name:    "adjustable without bounds",
			spec:    ThresholdSpec{Value: int64(4), ValueType: ThresholdInt64, Category: AdjustableThreshold},
			wantErr: "must declare ValueMin or ValueMax",
		},
		{
			name:    "value of the wrong type",
			spec:    ThresholdSpec{Value: "four", ValueType: ThresholdInt64, Category: DefaultThreshold},
			wantErr: "expected an integer, got string",
		},
		{
			name:    "lossy value",
			spec:    ThresholdSpec{Value: 3.5, ValueType: ThresholdInt64, Category: DefaultThreshold},
			wantErr: "expected an integer, got 3.5",
		},
		{
			name: "default outside its own bounds",
			spec: ThresholdSpec{
				Value:     int64(20),
				ValueType: ThresholdInt64,
				ValueMax:  int64(15),
				Category:  DefaultThreshold,
			},
			wantErr: "above the maximum 15",
		},
		{
			name: "per-architecture value of the wrong type",
			spec: ThresholdSpec{
				PerArchitecture: map[string]any{"GB200": "lots"},
				ValueType:       ThresholdInt64,
				Category:        DefaultThreshold,
			},
			archs:   []string{"GB200"},
			wantErr: `PerArchitecture["GB200"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cd := CheckDefinition{
				ID:            "test_check",
				Architectures: tt.archs,
				Thresholds:    map[string]ThresholdSpec{"expected_count": tt.spec},
			}
			err := cd.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// Bounds are independently optional, so one-sided ranges must validate.
func TestCheckDefinition_ValidateAcceptsOneSidedBounds(t *testing.T) {
	cd := CheckDefinition{
		ID: "test_check",
		Thresholds: map[string]ThresholdSpec{
			"max_inactive": {
				Value:     int64(0),
				ValueType: ThresholdInt64,
				ValueMin:  int64(0),
				Category:  DefaultThreshold,
			},
		},
	}
	if err := cd.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// A bare literal is the ergonomic way to author a threshold, so Validate
// must accept the int that `0` produces for an int64 threshold.
func TestCheckDefinition_ValidateAcceptsBareLiterals(t *testing.T) {
	cd := CheckDefinition{
		ID: "test_check",
		Thresholds: map[string]ThresholdSpec{
			"expected_count": {Value: 4, ValueType: ThresholdInt64, Category: DefaultThreshold},
			"min_power_w":    {Value: 65, ValueType: ThresholdFloat64, Category: DefaultThreshold},
		},
	}
	if err := cd.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
