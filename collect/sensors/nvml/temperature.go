// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"context"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

// The temperature signal family, in strict parity with gpud's
// components/accelerator/nvidia/temperature readings: current core and HBM
// temperatures, the driver-defined slowdown margin, and the four thermal
// thresholds. gpud's derived UsedPercent* ratios are health judgment and
// belong to the catalog, not the sensor.
func temperatureSignals() collect.Signals {
	return collect.Signals{
		gpusignals.TemperatureCelsius,
		gpusignals.TemperatureHBMCelsius,
		gpusignals.TemperatureSlowdownMarginCelsius,
		gpusignals.TemperatureThresholdShutdownCelsius,
		gpusignals.TemperatureThresholdSlowdownCelsius,
		gpusignals.TemperatureThresholdMemMaxCelsius,
		gpusignals.TemperatureThresholdGPUMaxCelsius,
	}
}

// NVML_TEMPERATURE_MEM is not exposed in go-nvml v0.13.0-1 yet.
// Use the nvml.h enum value to query memory (HBM/GDDR) temperature when supported.
// ref. https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceQueries.html#group__nvmlDeviceQueries_1g92d1c5182a14dd4be7090e3c1480b121
const temperatureSensorMemory nvml.TemperatureSensors = 1

// measureTemperature appends one observation per selected temperature signal
// for one device. A GPU-lost or requires-reset return aborts the remaining
// reads for the device (parity with gpud's GetTemperature short-circuit) and
// marks every uncollected selected signal of the family unavailable.
func measureTemperature(ctx context.Context, batch *observation.ObservationBatch, now *timestamppb.Timestamp, target *observation.ObservationTarget, dev device.Device, want map[string]bool) nvml.Return {
	reads := temperatureReads{
		batch:     batch,
		now:       now,
		target:    target,
		want:      want,
		collected: make(map[string]bool, len(temperatureSignals())),
	}

	// ref. https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceQueries.html#group__nvmlDeviceQueries_1g92d1c5182a14dd4be7090e3c1480b121
	if want[gpusignals.TemperatureCelsius.ID()] {
		if ctx.Err() != nil {
			return nvml.SUCCESS
		}
		temp, ret := dev.GetTemperature(nvml.TEMPERATURE_GPU)
		reads.record(gpusignals.TemperatureCelsius, "GetTemperature", ret, int64(temp))
		if reads.deviceUnreadable(ret) {
			return ret
		}
	}

	if want[gpusignals.TemperatureHBMCelsius.ID()] {
		if ctx.Err() != nil {
			return nvml.SUCCESS
		}
		tempHBM, ret := dev.GetTemperature(temperatureSensorMemory)
		reads.record(gpusignals.TemperatureHBMCelsius, "GetTemperature(memory)", ret, int64(tempHBM))
		if reads.deviceUnreadable(ret) {
			return ret
		}
	}

	// nvmlDeviceGetMarginTemperature returns the thermal margin (°C) to the nearest
	// slowdown threshold as defined by NVML. NVML does not specify GPU core vs HBM;
	// it is whichever slowdown threshold is nearest (driver-defined). The device
	// wrapper gates the call on driver >= 570, returning ERROR_NOT_SUPPORTED below it.
	// ref. https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceQueries.html#group__nvmlDeviceQueries_1g42db93dc04fc99d253eadc2037a5232d
	if want[gpusignals.TemperatureSlowdownMarginCelsius.ID()] {
		if ctx.Err() != nil {
			return nvml.SUCCESS
		}
		margin, ret := dev.GetMarginTemperature()
		reads.record(gpusignals.TemperatureSlowdownMarginCelsius, "GetMarginTemperature", ret, int64(margin.MarginTemperature))
		if reads.deviceUnreadable(ret) {
			return ret
		}
	}

	// ref. https://docs.nvidia.com/deploy/nvml-api/group__nvmlDeviceQueries.html#group__nvmlDeviceQueries_1g271ba78911494f33fc079b204a929405
	thresholds := []struct {
		definition collect.Signal
		threshold  nvml.TemperatureThresholds
	}{
		{gpusignals.TemperatureThresholdShutdownCelsius, nvml.TEMPERATURE_THRESHOLD_SHUTDOWN},
		{gpusignals.TemperatureThresholdSlowdownCelsius, nvml.TEMPERATURE_THRESHOLD_SLOWDOWN},
		// same signal DCGM "VerifyHBMTemperature" alerts on ("DCGM_FR_TEMP_VIOLATION"),
		// "DCGM_FI_DEV_MEM_MAX_OP_TEMP" == "NVML_TEMPERATURE_THRESHOLD_MEM_MAX"
		// ref. https://github.com/NVIDIA/DCGM/blob/a33560c9c138c617f3ee6cb50df11561302e5743/dcgmlib/src/DcgmCacheManager.cpp#L7738-L7767
		{gpusignals.TemperatureThresholdMemMaxCelsius, nvml.TEMPERATURE_THRESHOLD_MEM_MAX},
		{gpusignals.TemperatureThresholdGPUMaxCelsius, nvml.TEMPERATURE_THRESHOLD_GPU_MAX},
	}
	for _, th := range thresholds {
		if !want[th.definition.ID()] {
			continue
		}
		if ctx.Err() != nil {
			return nvml.SUCCESS
		}
		limit, ret := dev.GetTemperatureThreshold(th.threshold)
		reads.record(th.definition, "GetTemperatureThreshold", ret, int64(limit))
		if reads.deviceUnreadable(ret) {
			return ret
		}
	}
	return nvml.SUCCESS
}

// temperatureReads carries one device's read state through the temperature
// family: the shared batch context plus per-device selection and collection
// tracking.
type temperatureReads struct {
	want      map[string]bool
	collected map[string]bool
	batch     *observation.ObservationBatch
	now       *timestamppb.Timestamp
	target    *observation.ObservationTarget
}

// record appends the observation for one temperature read: the value on
// SUCCESS, otherwise a collection error categorized from the native return.
func (r *temperatureReads) record(definition collect.Signal, operation string, ret nvml.Return, value int64) {
	signalID := definition.ID()
	r.collected[signalID] = true
	if ret == nvml.SUCCESS {
		r.batch.Observations = append(r.batch.Observations, &observation.Observation{
			SignalId:   signalID,
			Source:     source,
			Target:     r.target,
			ObservedAt: r.now,
			Unit:       definition.Unit(),
			Outcome: &observation.Observation_Value{Value: &observation.Value{
				Kind: &observation.Value_IntValue{IntValue: value},
			}},
		})
		return
	}
	r.batch.Observations = append(r.batch.Observations, errorObservation(
		r.now, r.target, definition, operation, categorizeFailure(definition, ret),
		returnName(ret), nvml.ErrorString(ret)))
}

// deviceUnreadable reports whether the return means the GPU is lost or
// requires reset. Mirroring gpud: the remaining reads for the device are
// aborted, and every uncollected selected signal of the family becomes an
// UNAVAILABLE error carrying the same native return code.
func (r *temperatureReads) deviceUnreadable(ret nvml.Return) bool {
	if !isGPULost(ret) && !isGPURequiresReset(ret) {
		return false
	}
	for _, definition := range temperatureSignals() {
		signalID := definition.ID()
		if r.want[signalID] && !r.collected[signalID] {
			r.batch.Observations = append(r.batch.Observations, errorObservation(
				r.now, r.target, definition, "temperature",
				observation.CollectionErrorCategoryUnavailable, returnName(ret), nvml.ErrorString(ret)))
		}
	}
	return true
}

// categorizeFailure maps a failed NVML read to its
// collection-error category.
func categorizeFailure(definition collect.Signal, ret nvml.Return) observation.CollectionErrorCategory {
	switch {
	case isGPULost(ret), isGPURequiresReset(ret), ret == nvml.ERROR_UNINITIALIZED, ret == nvml.ERROR_NO_DATA, ret == nvml.ERROR_NOT_FOUND:
		return observation.CollectionErrorCategoryUnavailable
	case ret == nvml.ERROR_NO_PERMISSION:
		return observation.CollectionErrorCategoryPermissionDenied
	case ret == nvml.ERROR_TIMEOUT:
		return observation.CollectionErrorCategoryTimeout
	case ret == nvml.ERROR_NOT_SUPPORTED || ret == nvml.ERROR_FUNCTION_NOT_FOUND:
		return observation.CollectionErrorCategoryUnsupported
	case ret == nvml.ERROR_INVALID_ARGUMENT &&
		(definition == gpusignals.TemperatureHBMCelsius || definition == gpusignals.TemperatureSlowdownMarginCelsius):
		// gpud treats INVALID_ARGUMENT as "not supported" for the memory
		// and margin temperature queries
		return observation.CollectionErrorCategoryUnsupported
	default:
		return observation.CollectionErrorCategorySourceError
	}
}
