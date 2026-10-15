// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observations

import (
	"slices"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	processsignals "github.com/dsx-ai-factory/health-validation/collect/signals/process"
)

func TestToMeasurementsUnsignedPstate(t *testing.T) {
	for _, value := range []uint64{0, 7, uint64(^uint64(0) >> 1)} {
		observations := replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(),
			valueObservation(gpusignals.PerformanceState.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}))
		measurement := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
		if measurement.GPUPstate != int64(value) {
			t.Fatalf("pstate = %d, want %d", measurement.GPUPstate, value)
		}
		if slices.Contains(failedSignals(measurement), gpu.SignalGPUPstate) {
			t.Fatalf("fitting unsigned pstate failed: %v", measurement.Errors())
		}
	}
}

func TestToMeasurementsRejectsUnsignedOverflow(t *testing.T) {
	for _, value := range []uint64{1 << 63, ^uint64(0)} {
		observations := replace(healthyGPUObservations(), gpusignals.PerformanceState.ID(),
			valueObservation(gpusignals.PerformanceState.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: value}}))
		measurement := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
		if measurement.GPUPstate != 0 || !slices.Contains(failedSignals(measurement), gpu.SignalGPUPstate) {
			t.Fatalf("unsigned overflow wrapped or passed: pstate %d, errors %v", measurement.GPUPstate, measurement.Errors())
		}
	}
}

func TestToMeasurementsUnsignedFloat(t *testing.T) {
	observations := replace(healthyGPUObservations(), gpusignals.PowerDraw.ID(),
		valueObservation(gpusignals.PowerDraw.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: 75}}))
	measurement := only[gpu.GPUMeasurement](t, ToMeasurements(observations))
	if measurement.AveragePowerDrawW != 75 || slices.Contains(failedSignals(measurement), gpu.SignalAveragePowerDrawW) {
		t.Fatalf("unsigned power conversion failed: %+v, errors %v", measurement, measurement.Errors())
	}
}

func TestToMeasurementsLeavesRawProcessSignalsUnmapped(t *testing.T) {
	const bootID = "01234567-89ab-cdef-0123-456789abcdef"
	processState := valueObservation(processsignals.State.ID(), &observation.Value{Kind: &observation.Value_StringValue{StringValue: "D"}})
	processState.Target = observation.ProcessTarget(bootID, 42, 500)
	gpuMemory := valueObservation(gpusignals.ProcessMemoryUsed.ID(), &observation.Value{Kind: &observation.Value_UintValue{UintValue: ^uint64(0)}})
	gpuMemory.Target = observation.GPUProcessTarget("GPU-0", bootID, 42, 500)
	raw := []*observation.Observation{processState, gpuMemory}
	if err := observation.ValidateBatch(&observation.ObservationBatch{Observations: raw}); err != nil {
		t.Fatalf("raw process observations rejected: %v", err)
	}
	if measurements := ToMeasurements(raw); len(measurements) != 0 {
		t.Fatalf("raw process observations falsely mapped into catalog: %v", measurements)
	}
	measurement := only[gpu.GPUMeasurement](t, ToMeasurements(append(healthyGPUObservations(), raw...)))
	if measurement.GPUID != "GPU-0" || measurement.AveragePowerDrawW != 75 || slices.Contains(failedSignals(measurement), gpu.SignalAveragePowerDrawW) {
		t.Fatalf("raw process signals polluted GPU measurement: %+v", measurement)
	}
}
