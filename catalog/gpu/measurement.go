// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpu

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// GPU signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in GPU
// check definitions.
const (
	SignalGPUID                  = "gpu_id"
	SignalGPUPstate              = "gpu_pstate"
	SignalAveragePowerDrawW      = "average_power_draw_w"
	SignalPowerLimitW            = "power_limit_w"
	SignalEnforcedPowerLimitW    = "enforced_power_limit_w"
	SignalHWSlowdown             = "hw_slowdown"
	SignalHWThermalSlowdown      = "hw_thermal_slowdown"
	SignalHWPowerBrakeSlowdown   = "hw_power_brake_slowdown"
	SignalComputeProcessCount    = "compute_process_count"
	SignalComputeProcessCmdlines = "compute_process_cmdlines"
)

var measurementSignals = []string{
	SignalGPUID,
	SignalGPUPstate,
	SignalAveragePowerDrawW,
	SignalPowerLimitW,
	SignalEnforcedPowerLimitW,
	SignalHWSlowdown,
	SignalHWThermalSlowdown,
	SignalHWPowerBrakeSlowdown,
	SignalComputeProcessCount,
	SignalComputeProcessCmdlines,
}

// MeasurementSignals lists every signal a GPUMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot
// observe the GPU at all (e.g. a device handle that failed to load),
// telling the catalog that no signal is trustworthy for this entity.
// The returned slice is a copy.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// GPUMeasurement is a single observation of one GPU's health state.
// Partners populate this struct from their telemetry source (nvidia-smi,
// DCGM, NVML, or custom) and pass it to catalog evaluation.
//
//	m := gpu.GPUMeasurement{GPUID: "GPU-0"}
//	verdict, err := cat.EvaluateAll(m)
//
// Sensors that encounter per-field collection failures call AddError
// for each failing query, naming the signals that query would have
// populated. The catalog uses the accumulated error list to decide
// per check whether to fail Critical or run CEL against the partial
// data. For whole-entity failures (the sensor couldn't even identify
// the GPU), build the measurement with [ErroredGPU].
type GPUMeasurement struct {
	GPUID string `json:"gpu_id"`
	// ComputeProcessCmdlines is a diagnostic summary of compute-context
	// PIDs still holding this GPU (pid plus quoted /proc cmdline when
	// readable), one process per line.
	ComputeProcessCmdlines string `json:"compute_process_cmdlines"`
	entity                 health.EntityKey
	errors                 []health.ErroredMeasurement
	unsupported            []string
	AveragePowerDrawW      float64 `json:"average_power_draw_w"`
	PowerLimitW            float64 `json:"power_limit_w"`
	EnforcedPowerLimitW    float64 `json:"enforced_power_limit_w"`
	// ComputeProcessCount is the number of compute (CUDA) contexts NVML
	// reports on this GPU. Graphics and other process classes are omitted.
	ComputeProcessCount int64 `json:"compute_process_count"`
	// GPUPstate is the GPU's current performance state as the numeric
	// level N in P<N>: 0 is P0 (maximum performance) and higher values
	// are progressively lower-power states.
	GPUPstate int64 `json:"gpu_pstate"`
	// HWSlowdown, HWThermalSlowdown, and HWPowerBrakeSlowdown report NVML
	// clocks-event-reasons bits: whether hardware protection logic (generic,
	// thermal, or external power-brake respectively) is actively clamping
	// the GPU's clocks.
	HWSlowdown           bool `json:"hw_slowdown"`
	HWThermalSlowdown    bool `json:"hw_thermal_slowdown"`
	HWPowerBrakeSlowdown bool `json:"hw_power_brake_slowdown"`
}

// ErroredGPU returns a GPUMeasurement marked errored for a whole-GPU
// failure with one initial error entry. Pass [MeasurementSignals]…
// for failedSignals when the entire entity is unobservable. For
// partial failures, populate the struct directly and call
// [GPUMeasurement.AddError] for each failing query.
//
// gpuID identifies the affected GPU (e.g. a UUID or bus position) so
// downstream consumers can correlate the verdict with the underlying
// device. err describes why the measurement failed — sensors typically
// pass a domain-specific error type (e.g. nvml.Error) so consumers can
// type-assert for structured failure details.
func ErroredGPU(gpuID string, err error, failedSignals ...string) GPUMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	m := GPUMeasurement{GPUID: gpuID}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement.
// Sensors call this for each failing query, listing the signals the
// failing call would have populated. Multiple AddError calls
// accumulate, so a single measurement can carry independent failures
// from different fields. err must be non-nil and is surfaced in the
// resulting verdict's message via its Error() string; pass a
// domain-specific error type (e.g. nvml.Error) so consumers can
// type-assert for structured failure details.
func (m *GPUMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (GPUMeasurement) Category() health.Category { return health.GPU }

// EntityID implements [health.EntityIdentifier].
func (m GPUMeasurement) EntityID() string { return m.GPUID }

// EntityKey implements [health.EntityKeyed]. Without a key from
// [GPUMeasurement.SetEntityKey], a non-empty GPUID is taken as a verified ID and an
// empty one as unidentified.
func (m GPUMeasurement) EntityKey() health.EntityKey {
	switch {
	case m.entity != health.EntityKey{}:
		return m.entity
	case m.GPUID == "":
		return health.EntityKey{}
	default:
		return health.VerifiedEntity(m.GPUID)
	}
}

// SetEntityKey records the identity evaluation keys this measurement by. Set
// it whenever GPUID is not a verified UUID, such as a slot whose UUID could
// not be read; GPUID then only labels the GPU in verdicts.
func (m *GPUMeasurement) SetEntityKey(k health.EntityKey) { m.entity = k }

// Errors implements [health.Measurement].
func (m GPUMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// MarkUnsupported records signals the source reported as unsupported.
// Their fields keep the value the sensor set, so evaluation is unchanged.
func (m *GPUMeasurement) MarkUnsupported(signals ...string) {
	m.unsupported = append(m.unsupported, signals...)
}

// UnsupportedSignals implements [health.UnsupportedSignaler].
func (m GPUMeasurement) UnsupportedSignals() []string { return m.unsupported }
