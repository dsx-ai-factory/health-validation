// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package node

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Node-level signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in node
// check definitions.
const (
	SignalGPUCount = "gpu_count"
)

var measurementSignals = []string{
	SignalGPUCount,
}

// MeasurementSignals lists every signal a NodeMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot
// observe the node at all (e.g. NVML failed to initialize), telling
// the catalog that no signal is trustworthy for this entity.
// The returned slice is a copy.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// NodeMeasurement captures host-wide signals that are not bound to any
// single GPU — for example, the number of GPUs the driver enumerated.
// Sensors emit one NodeMeasurement per collection pass; per-GPU data
// lives in [gpu.GPUMeasurement].
//
//	m := node.NodeMeasurement{GPUCount: 8}
//	verdict, err := cat.EvaluateAll(m)
//
// Sensors that encounter per-field collection failures call AddError
// for each failing query, naming the signals that query would have
// populated. For whole-entity failures (the sensor couldn't observe
// the node at all), build the measurement with [ErroredNode].
type NodeMeasurement struct {
	errors   []health.ErroredMeasurement
	GPUCount int64 `json:"gpu_count"`
}

// ErroredNode returns a NodeMeasurement marked errored for a whole-node
// failure with one initial error entry. Pass [MeasurementSignals]…
// for failedSignals when the entire node is unobservable. For partial
// failures, populate the struct directly and call
// [NodeMeasurement.AddError] for each failing query.
//
// err describes why the measurement failed — sensors typically pass a
// domain-specific error type (e.g. nvml.Error) so consumers can
// type-assert for structured failure details.
func ErroredNode(err error, failedSignals ...string) NodeMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	var m NodeMeasurement
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement.
// Sensors call this for each failing query, listing the signals the
// failing call would have populated. Multiple AddError calls
// accumulate, so a single measurement can carry independent failures
// from different fields. err is surfaced in the resulting verdict's
// message via its Error() string; pass a domain-specific error type
// (e.g. nvml.Error) so consumers can type-assert for structured
// failure details.
func (m *NodeMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (NodeMeasurement) Category() health.Category { return health.Node }

// Errors implements [health.Measurement].
func (m NodeMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
