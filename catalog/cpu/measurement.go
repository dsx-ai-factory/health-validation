// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package cpu

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

const (
	// SignalPossibleCPUCount is the number of logical CPU IDs in the kernel's possible mask.
	SignalPossibleCPUCount = "possible_cpu_count"

	// SignalOnlineCPUCount is the number of logical CPUs available for scheduling.
	SignalOnlineCPUCount = "online_cpu_count"

	// SignalNUMANodeCount is the number of NUMA nodes the kernel brings online.
	SignalNUMANodeCount = "numa_node_count"
)

var measurementSignals = []string{
	SignalPossibleCPUCount,
	SignalOnlineCPUCount,
	SignalNUMANodeCount,
}

// MeasurementSignals lists every signal a CPUMeasurement carries.
// The returned slice is a copy.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// CPUMeasurement captures host-wide CPU availability and topology.
type CPUMeasurement struct {
	errors           []health.ErroredMeasurement
	PossibleCPUCount int64 `json:"possible_cpu_count"`
	OnlineCPUCount   int64 `json:"online_cpu_count"`
	NUMANodeCount    int64 `json:"numa_node_count"`
}

// ErroredCPU returns a measurement marked errored for a whole-source failure.
// An unattributed failure applies to every CPU signal.
func ErroredCPU(err error, failedSignals ...string) CPUMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	var m CPUMeasurement
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection failure to the measurement.
func (m *CPUMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (CPUMeasurement) Category() health.Category { return health.CPU }

// Errors implements [health.Measurement].
func (m CPUMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
