//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"encoding/binary"
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
			got := test.transform(value)
			if got.CollectionError != nil {
				t.Fatalf("transform() collection error = %v", got.CollectionError)
			}
			if got.Value.GetBoolValue() != test.want {
				t.Errorf("transform() = %v, want %v", got.Value.GetBoolValue(), test.want)
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
			got := fabricHealthFlag(test.shift)(intField(mask))
			if got.CollectionError != nil {
				t.Fatalf("fabricHealthFlag() collection error = %v", got.CollectionError)
			}
			if got.Value.GetBoolValue() != test.want {
				t.Errorf("fabricHealthFlag() = %v, want %v", got.Value.GetBoolValue(), test.want)
			}
		})
	}
}

func TestFabricHealthTransformPreservesUnsupported(t *testing.T) {
	got := fabricHealthFlag(0)(intField(0))
	if got.CollectionError != nil || got.Value != nil || got.NoValue == nil {
		t.Fatalf("fabricHealthFlag() = %+v, want no-value only", got)
	}
	if got.NoValue.GetReason() != observation.NoValueReasonNotSupported {
		t.Errorf("reason = %v, want %v", got.NoValue.GetReason(), observation.NoValueReasonNotSupported)
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

func TestDecodeMetricValueFabricNotSupported(t *testing.T) {
	fabric := []string{
		gpusignals.FabricManagerStatus.ID(),
		gpusignals.NVLinkFabricBandwidthDegraded.ID(),
		gpusignals.NVLinkFabricRouteRecoveryActive.ID(),
		gpusignals.NVLinkFabricRouteUnhealthy.ID(),
		gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(),
	}
	for _, signalID := range fabric {
		got := decodeMetricValue(fieldsBySignal[signalID], int64FieldValue(godcgm.DCGM_FT_INT64_NOT_SUPPORTED))
		if got.CollectionError != nil || got.Value != nil || got.NoValue == nil {
			t.Fatalf("%s: decodeMetricValue() = %+v, want no-value only", signalID, got)
		}
		if got.NoValue.GetReason() != observation.NoValueReasonNotSupported || got.NoValue.GetSourceStatus() != "DCGM_FT_NOT_SUPPORTED" {
			t.Errorf("%s: no value = %+v", signalID, got.NoValue)
		}
	}

	status := fieldsBySignal[gpusignals.FabricManagerStatus.ID()]
	if got := decodeMetricValue(fieldsBySignal[gpusignals.PerformanceState.ID()], int64FieldValue(godcgm.DCGM_FT_INT64_NOT_SUPPORTED)); got.CollectionError.GetCategory() != observation.CollectionErrorCategoryUnsupported {
		t.Errorf("non-fabric NOT_SUPPORTED = %+v, want an unsupported collection error", got)
	}
	for sentinel, want := range map[int64]observation.CollectionErrorCategory{
		godcgm.DCGM_FT_INT64_BLANK:            observation.CollectionErrorCategoryUnavailable,
		godcgm.DCGM_FT_INT64_NOT_FOUND:        observation.CollectionErrorCategoryUnavailable,
		godcgm.DCGM_FT_INT64_NOT_PERMISSIONED: observation.CollectionErrorCategoryPermissionDenied,
	} {
		if got := decodeMetricValue(status, int64FieldValue(sentinel)); got.NoValue != nil || got.CollectionError.GetCategory() != want {
			t.Errorf("fabric sentinel %d = %+v, want a %v collection error", sentinel, got, want)
		}
	}
	if got := decodeMetricValue(status, int64FieldValue(0)); got.Value == nil || got.Value.GetIntValue() != 0 {
		t.Errorf("fabric status 0 = %+v, want the integer 0", got)
	}
}

// int64FieldValue is a successful INT64 sample carrying value.
func int64FieldValue(value int64) godcgm.FieldValue_v1 {
	field := godcgm.FieldValue_v1{FieldType: godcgm.DCGM_FT_INT64, Status: godcgm.DCGM_ST_OK, TS: 1}
	binary.LittleEndian.PutUint64(field.Value[:8], uint64(value))
	return field
}

func intField(value int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: value}}
}
