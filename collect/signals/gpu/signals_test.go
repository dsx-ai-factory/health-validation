// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import (
	"testing"

	"github.com/dsx-ai-factory/health-validation/collect"
)

func TestCatalog(t *testing.T) {
	seen := make(map[string]struct{})
	for _, definition := range All() {
		id := definition.ID()
		if id == "" {
			t.Fatal("signal has an empty ID")
		}
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("signal ID %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
}

func TestMetricSemantics(t *testing.T) {
	tests := []struct {
		definition collect.Signal
		id         string
		unit       string
	}{
		{ReliabilityViolationDuration, "gpu.reliability.violation.duration", "nanoseconds"},
		{BoardLimitViolationDuration, "gpu.board_limit.violation.duration", "nanoseconds"},
		{NVLinkThroughputTotal, "gpu.nvlink.throughput.total", "megabytes_per_second"},
	}
	for _, test := range tests {
		if got := test.definition.ID(); got != test.id {
			t.Errorf("Signal.ID() = %q, want %q", got, test.id)
		}
		unit := test.definition.Unit()
		if unit == nil || *unit != test.unit {
			t.Errorf("Signal.Unit() = %v, want %q", unit, test.unit)
		}
	}
}
