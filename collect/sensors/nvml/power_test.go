// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type fakeDevice struct {
	device.Device
	calls             []string
	powerReturn       nvml.Return
	temperatureReturn nvml.Return
}

func (d *fakeDevice) GetPowerUsage() (uint32, nvml.Return) {
	d.calls = append(d.calls, "usage")
	return 123456, d.powerReturn
}

func (d *fakeDevice) GetEnforcedPowerLimit() (uint32, nvml.Return) {
	d.calls = append(d.calls, "enforced")
	return 250000, nvml.SUCCESS
}

func (d *fakeDevice) GetPowerManagementLimit() (uint32, nvml.Return) {
	d.calls = append(d.calls, "requested")
	return 275000, nvml.SUCCESS
}

func (d *fakeDevice) GetTemperature(sensor nvml.TemperatureSensors) (uint32, nvml.Return) {
	d.calls = append(d.calls, "temperature")
	return 42 + uint32(sensor), d.temperatureReturn
}

func (d *fakeDevice) GetMarginTemperature() (nvml.MarginTemperature, nvml.Return) {
	d.calls = append(d.calls, "margin")
	return nvml.MarginTemperature{MarginTemperature: -3}, nvml.SUCCESS
}

func (d *fakeDevice) GetTemperatureThreshold(nvml.TemperatureThresholds) (uint32, nvml.Return) {
	d.calls = append(d.calls, "threshold")
	return 90, nvml.SUCCESS
}

func TestPowerCollection(t *testing.T) {
	for _, ret := range []nvml.Return{nvml.SUCCESS, nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_GPU_IS_LOST, nvml.ERROR_RESET_REQUIRED, nvml.ERROR_UNKNOWN} {
		t.Run(returnName(ret), func(t *testing.T) {
			dev := &fakeDevice{powerReturn: ret}
			s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu-1": dev}})
			batch, err := s.Measure(context.Background(), collect.WithSignalID(powerSignals().IDs()...))
			if err != nil {
				t.Fatal(err)
			}
			if validationErr := observation.ValidateBatch(batch); validationErr != nil {
				t.Fatal(validationErr)
			}
			if len(batch.Observations) != 3 {
				t.Fatalf("observations: %v", batch)
			}
			first := batch.Observations[0]
			if first.GetEntity().GetId() != "gpu-1" || first.GetUnit() != "watts" {
				t.Fatalf("envelope: %v", first)
			}
			switch ret {
			case nvml.SUCCESS:
				if first.GetValue().GetDoubleValue() != 123.456 || batch.Observations[1].GetValue().GetDoubleValue() != 250 || batch.Observations[2].GetValue().GetDoubleValue() != 275 {
					t.Fatalf("watts: %v", batch)
				}
			case nvml.ERROR_NOT_SUPPORTED:
				if first.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnsupported {
					t.Fatalf("unsupported: %v", first)
				}
			default:
				if first.GetCollectionError().GetSourceError() != returnName(ret) {
					t.Fatalf("native return lost: %v", first)
				}
			}
			if ret == nvml.ERROR_GPU_IS_LOST || ret == nvml.ERROR_RESET_REQUIRED {
				if len(dev.calls) != 1 || batch.Observations[2].GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
					t.Fatalf("did not stop reads: %v / %v", dev.calls, batch)
				}
			} else if len(dev.calls) != 3 {
				t.Fatalf("calls: %v", dev.calls)
			}
		})
	}
}

func TestPowerAndTemperatureSelection(t *testing.T) {
	dev := &fakeDevice{}
	s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu-1": dev}})
	batch, err := s.Measure(context.Background(), collect.WithSignalID(gpusignals.PowerLimitRequested.ID(), gpusignals.TemperatureSlowdownMarginCelsius.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if len(dev.calls) != 2 || len(batch.Observations) != 2 || batch.Observations[0].GetValue().GetIntValue() != -3 {
		t.Fatalf("selected reads: %v / %v", dev.calls, batch)
	}
	dev.calls = nil
	batch, err = s.Measure(context.Background())
	if err != nil || len(batch.GetObservations()) != 10 || len(dev.calls) != 10 {
		t.Fatalf("full power/temperature coverage: %v / %v / %v", dev.calls, batch, err)
	}
	if _, err = s.Measure(context.Background(), collect.WithSignalID("unknown")); err == nil {
		t.Fatal("accepted unknown signal")
	}
}

func TestLostGPUStopsAllFamilies(t *testing.T) {
	for _, ret := range []nvml.Return{nvml.ERROR_GPU_IS_LOST, nvml.ERROR_RESET_REQUIRED} {
		dev := &fakeDevice{temperatureReturn: ret}
		s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}})
		batch, err := s.Measure(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(dev.calls) != 1 || len(batch.Observations) != len(s.Signals()) {
			t.Fatalf("continued reading lost GPU: %v / %v", dev.calls, batch)
		}
		for _, current := range batch.Observations[1:] {
			if current.GetCollectionError().GetCategory() != observation.CollectionErrorCategoryUnavailable {
				t.Fatalf("missing unavailable observation: %v", current)
			}
		}
	}
}
