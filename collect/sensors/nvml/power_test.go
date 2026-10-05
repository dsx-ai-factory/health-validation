// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

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
			require.NoError(t, err)
			require.NoError(t, observation.ValidateBatch(batch))
			require.Len(t, batch.Observations, 3)
			first := batch.Observations[0]
			require.Equal(t, "gpu-1", first.GetTarget().GetGpu().GetUuid())
			require.Equal(t, "watts", first.GetUnit())
			switch ret {
			case nvml.SUCCESS:
				require.Equal(t, float64(123.456), first.GetValue().GetDoubleValue())
				require.Equal(t, float64(250), batch.Observations[1].GetValue().GetDoubleValue())
				require.Equal(t, float64(275), batch.Observations[2].GetValue().GetDoubleValue())
			case nvml.ERROR_NOT_SUPPORTED:
				require.Equal(t, observation.CollectionErrorCategoryUnsupported, first.GetCollectionError().GetCategory())
			default:
				require.Equal(t, returnName(ret), first.GetCollectionError().GetSourceError())
			}
			if ret == nvml.ERROR_GPU_IS_LOST || ret == nvml.ERROR_RESET_REQUIRED {
				require.Len(t, dev.calls, 1)
				require.Equal(t, observation.CollectionErrorCategoryUnavailable, batch.Observations[2].GetCollectionError().GetCategory())
			} else {
				require.Len(t, dev.calls, 3)
			}
		})
	}
}

func TestPowerAndTemperatureSelection(t *testing.T) {
	dev := &fakeDevice{}
	s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu-1": dev}})
	batch, err := s.Measure(context.Background(), collect.WithSignalID(gpusignals.PowerLimitRequested.ID(), gpusignals.TemperatureSlowdownMarginCelsius.ID()))
	require.NoError(t, err)
	require.Len(t, dev.calls, 2)
	require.Len(t, batch.Observations, 2)
	require.Equal(t, int64(-3), batch.Observations[0].GetValue().GetIntValue())
	dev.calls = nil
	batch, err = s.Measure(context.Background())
	require.NoError(t, err)
	require.Len(t, batch.GetObservations(), 10)
	require.Len(t, dev.calls, 10)
	_, err = s.Measure(context.Background(), collect.WithSignalID("unknown"))
	require.Error(t, err)
}

func TestLostGPUStopsAllFamilies(t *testing.T) {
	for _, ret := range []nvml.Return{nvml.ERROR_GPU_IS_LOST, nvml.ERROR_RESET_REQUIRED} {
		dev := &fakeDevice{temperatureReturn: ret}
		s := New(&fakeInstance{exists: true, devices: map[string]device.Device{"gpu": dev}})
		batch, err := s.Measure(context.Background())
		require.NoError(t, err)
		require.Len(t, dev.calls, 1)
		require.Len(t, batch.Observations, len(s.Signals()))
		for _, current := range batch.Observations[1:] {
			require.Equal(t, observation.CollectionErrorCategoryUnavailable, current.GetCollectionError().GetCategory())
		}
	}
}
