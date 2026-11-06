// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"reflect"
	"testing"
)

func TestOverrideSet_ForCheck(t *testing.T) {
	os := NewOverrideSet(
		Override{CheckID: "a", ThresholdName: "x", Value: 1},
		Override{CheckID: "a", ThresholdName: "y", Value: 2},
		Override{CheckID: "b", ThresholdName: "z", Value: 3},
	)

	got := os.ForCheck("a")
	if len(got) != 2 {
		t.Errorf("got %d overrides for 'a', want 2", len(got))
	}
	if got["x"] != 1 || got["y"] != 2 {
		t.Errorf("unexpected values: %v", got)
	}
}

func TestOverrideSet_ForCheckMissing(t *testing.T) {
	os := NewOverrideSet()
	if got := os.ForCheck("nope"); got != nil {
		t.Errorf("expected nil for missing check, got %v", got)
	}
}

func TestCatalog_ConfiguredOverrides(t *testing.T) {
	c := New(
		WithArchitecture("GB200"),
		WithDisabledChecks("gpu_count"),
		WithOverrides(NewOverrideSet(
			Override{CheckID: "gpu_power_limit", ThresholdName: "expected_power_limit_w", Value: 1000},
			Override{CheckID: "gpu_count", ThresholdName: "expected_gpu_count", Value: 8},
			Override{CheckID: "gpu_count", ThresholdName: "no_such_threshold", Value: 1},
			Override{CheckID: "no_such_check", ThresholdName: "x", Value: 1},
		)),
	)

	got, err := c.ConfiguredOverrides()
	if err != nil {
		t.Fatalf("ConfiguredOverrides() error = %v", err)
	}
	// Sorted, coerced to each threshold's type, original values resolved for
	// GB200; the disabled check is kept and unknown targets are dropped.
	want := []ConfiguredOverride{
		{CheckID: "gpu_count", ThresholdName: "expected_gpu_count", Value: int64(8), OriginalValue: int64(4)},
		{CheckID: "gpu_power_limit", ThresholdName: "expected_power_limit_w", Value: float64(1000), OriginalValue: float64(1200)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ConfiguredOverrides() = %+v, want %+v", got, want)
	}
}
