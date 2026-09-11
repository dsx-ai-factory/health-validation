//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func TestClockEventTransforms(t *testing.T) {
	value := intField(
		clockEventReasonHardwareSlowdown |
			clockEventReasonHardwarePowerBrakeSlowdown,
	)
	tests := []struct {
		transform fieldTransform
		name      string
		want      bool
	}{
		{name: "hardware slowdown", transform: bitSet(clockEventReasonHardwareSlowdown), want: true},
		{name: "hardware thermal slowdown", transform: bitSet(clockEventReasonHardwareThermalSlowdown), want: false},
		{name: "hardware power brake", transform: bitSet(clockEventReasonHardwarePowerBrakeSlowdown), want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, failure := test.transform(value)
			if failure != nil {
				t.Fatalf("transform() collection error = %v", failure)
			}
			if got.GetBoolValue() != test.want {
				t.Errorf("transform() = %v, want %v", got.GetBoolValue(), test.want)
			}
		})
	}
}

func TestFabricHealthTransform(t *testing.T) {
	// Route recovery is active; the other fields explicitly report false.
	mask := int64(2 | (1 << 2) | (2 << 4) | (2 << 6))
	tests := []struct {
		name  string
		shift uint
		want  bool
	}{
		{name: "bandwidth", shift: 0, want: false},
		{name: "route recovery", shift: 2, want: true},
		{name: "route health", shift: 4, want: false},
		{name: "access timeout recovery", shift: 6, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, failure := fabricHealthFlag(test.shift)(intField(mask))
			if failure != nil {
				t.Fatalf("fabricHealthFlag() collection error = %v", failure)
			}
			if got.GetBoolValue() != test.want {
				t.Errorf("fabricHealthFlag() = %v, want %v", got.GetBoolValue(), test.want)
			}
		})
	}
}

func TestFabricHealthTransformPreservesUnsupported(t *testing.T) {
	_, failure := fabricHealthFlag(0)(intField(0))
	if failure == nil {
		t.Fatal("fabricHealthFlag() returned no collection error")
	}
	if failure.GetCategory() != observation.CollectionErrorCategoryUnsupported {
		t.Errorf("category = %v, want %v", failure.GetCategory(), observation.CollectionErrorCategoryUnsupported)
	}
	if failure.GetSourceError() != "" {
		t.Errorf("source error = %q, want empty", failure.GetSourceError())
	}
}

func TestDerivedSignalsShareDCGMFields(t *testing.T) {
	mappings, err := fieldMappingsFor([]string{
		gpusignals.ClockEventReasons.ID(),
		gpusignals.HardwareSlowdownActive.ID(),
		gpusignals.HardwareThermalSlowdownActive.ID(),
		gpusignals.HardwarePowerBrakeSlowdownActive.ID(),
	})
	if err != nil {
		t.Fatalf("fieldMappingsFor() error = %v", err)
	}
	fieldIDs := uniqueFieldIDs(mappings)
	if len(fieldIDs) != 1 || fieldIDs[0] != godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS {
		t.Fatalf("uniqueFieldIDs() = %v, want [%d]", fieldIDs, godcgm.DCGM_FI_DEV_CLOCKS_EVENT_REASONS)
	}
}

func intField(value int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: value}}
}
