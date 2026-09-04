// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

func newTemperatureReads(want map[string]bool) *temperatureReads {
	return &temperatureReads{
		batch:     &observation.ObservationBatch{},
		now:       timestamppb.Now(),
		entity:    observation.GPUEntity("test-gpu"),
		want:      want,
		collected: make(map[string]bool, len(temperatureSignals())),
	}
}

func TestCategorizeTemperatureFailure(t *testing.T) {
	tests := []struct {
		definition collect.Signal
		ret        nvml.Return
		want       observation.CollectionErrorCategory
	}{
		{gpusignals.TemperatureCelsius, nvml.ERROR_NOT_SUPPORTED, observation.CollectionErrorCategoryUnsupported},
		{gpusignals.TemperatureHBMCelsius, nvml.ERROR_INVALID_ARGUMENT, observation.CollectionErrorCategoryUnsupported},
		{gpusignals.TemperatureSlowdownMarginCelsius, nvml.ERROR_INVALID_ARGUMENT, observation.CollectionErrorCategoryUnsupported},
		{gpusignals.TemperatureCelsius, nvml.ERROR_INVALID_ARGUMENT, observation.CollectionErrorCategorySourceError},
		{gpusignals.TemperatureCelsius, nvml.ERROR_GPU_IS_LOST, observation.CollectionErrorCategorySourceError},
	}
	for _, test := range tests {
		if got := categorizeTemperatureFailure(test.definition, test.ret); got != test.want {
			t.Errorf("categorizeTemperatureFailure(%s, %s) = %v, want %v",
				test.definition.ID(), returnName(test.ret), got, test.want)
		}
	}
}

func TestRecord(t *testing.T) {
	t.Run("success appends a value observation", func(t *testing.T) {
		reads := newTemperatureReads(map[string]bool{gpusignals.TemperatureCelsius.ID(): true})
		reads.record(gpusignals.TemperatureCelsius, "GetTemperature", nvml.SUCCESS, 42)

		if len(reads.batch.Observations) != 1 {
			t.Fatalf("record() appended %d observations, want 1", len(reads.batch.Observations))
		}
		current := reads.batch.Observations[0]
		if current.GetSignalId() != gpusignals.TemperatureCelsius.ID() {
			t.Errorf("signal ID = %q, want %q", current.GetSignalId(), gpusignals.TemperatureCelsius.ID())
		}
		if got := current.GetValue().GetIntValue(); got != 42 {
			t.Errorf("value = %d, want 42", got)
		}
		if current.Unit == nil || *current.Unit != "celsius" {
			t.Errorf("unit = %v, want %q", current.Unit, "celsius")
		}
		if !reads.collected[gpusignals.TemperatureCelsius.ID()] {
			t.Error("record() did not mark the signal collected")
		}
	})

	t.Run("failure appends a categorized collection error", func(t *testing.T) {
		reads := newTemperatureReads(map[string]bool{gpusignals.TemperatureHBMCelsius.ID(): true})
		reads.record(gpusignals.TemperatureHBMCelsius, "GetTemperature(memory)", nvml.ERROR_INVALID_ARGUMENT, 0)

		if len(reads.batch.Observations) != 1 {
			t.Fatalf("record() appended %d observations, want 1", len(reads.batch.Observations))
		}
		failure := reads.batch.Observations[0].GetCollectionError()
		if failure == nil {
			t.Fatal("record() appended a value, want a collection error")
		}
		if failure.GetCategory() != observation.CollectionErrorCategoryUnsupported {
			t.Errorf("category = %v, want %v", failure.GetCategory(), observation.CollectionErrorCategoryUnsupported)
		}
		if failure.GetOperation() != "GetTemperature(memory)" {
			t.Errorf("operation = %q, want %q", failure.GetOperation(), "GetTemperature(memory)")
		}
	})
}

func TestDeviceUnreadable(t *testing.T) {
	t.Run("non-fatal return appends nothing", func(t *testing.T) {
		reads := newTemperatureReads(map[string]bool{gpusignals.TemperatureCelsius.ID(): true})
		if reads.deviceUnreadable(nvml.ERROR_NOT_SUPPORTED) {
			t.Error("deviceUnreadable(ERROR_NOT_SUPPORTED) = true, want false")
		}
		if len(reads.batch.Observations) != 0 {
			t.Fatalf("appended %d observations, want 0", len(reads.batch.Observations))
		}
	})

	t.Run("gpu lost marks every uncollected selected signal unavailable", func(t *testing.T) {
		reads := newTemperatureReads(map[string]bool{
			gpusignals.TemperatureCelsius.ID():                  true,
			gpusignals.TemperatureThresholdShutdownCelsius.ID(): true,
		})
		reads.record(gpusignals.TemperatureCelsius, "GetTemperature", nvml.SUCCESS, 42)

		if !reads.deviceUnreadable(nvml.ERROR_GPU_IS_LOST) {
			t.Fatal("deviceUnreadable(ERROR_GPU_IS_LOST) = false, want true")
		}
		if len(reads.batch.Observations) != 2 {
			t.Fatalf("appended %d observations, want 2 (one kept, one unavailable)", len(reads.batch.Observations))
		}
		failure := reads.batch.Observations[1].GetCollectionError()
		if failure == nil {
			t.Fatal("second observation has no collection error")
		}
		if failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
			t.Errorf("category = %v, want %v", failure.GetCategory(), observation.CollectionErrorCategoryUnavailable)
		}
		if failure.GetOperation() != "temperature" {
			t.Errorf("operation = %q, want %q", failure.GetOperation(), "temperature")
		}
	})
}
