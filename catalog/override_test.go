// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import "testing"

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
