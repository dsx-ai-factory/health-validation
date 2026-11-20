// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package pcie

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Signal names used by PCIe check rules.
const (
	SignalGPUID                 = "gpu_id"
	SignalPCIeMaxLinkWidth      = "max_link_width"
	SignalPCIeMaxLinkGeneration = "max_link_generation"
)

var measurementSignals = []string{SignalGPUID, SignalPCIeMaxLinkWidth, SignalPCIeMaxLinkGeneration}

// UnsupportedLinkValue represents an explicitly unsupported PCIe link property.
// Zero means uncollected; other negative values are invalid. Collectors must
// report failed reads with AddError rather than this value.
const UnsupportedLinkValue int64 = -1

// MeasurementSignals returns a copy of the PCIe measurement's signal names.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// PCIeMeasurement describes the maximum PCIe link capabilities of one GPU.
// Unsupported maxima use UnsupportedLinkValue; zero and other negative values
// indicate unavailable data rather than a link fault.
type PCIeMeasurement struct {
	GPUID                 string `json:"gpu_id"`
	entity                health.EntityKey
	errors                []health.ErroredMeasurement
	PCIeMaxLinkWidth      int64 `json:"max_link_width"`
	PCIeMaxLinkGeneration int64 `json:"max_link_generation"`
}

// ErroredPCIe marks the named signals as failed, or every signal when omitted.
func ErroredPCIe(gpuID string, err error, failedSignals ...string) PCIeMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	m := PCIeMeasurement{GPUID: gpuID}
	m.AddError(err, failedSignals...)
	return m
}

// AddError records a failure for the named signals.
func (m *PCIeMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (PCIeMeasurement) Category() health.Category { return health.PCIe }

// Errors implements [health.Measurement].
func (m PCIeMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// EntityID implements [health.EntityIdentifier].
func (m PCIeMeasurement) EntityID() string { return m.GPUID }

// EntityKey preserves source-local identities when a GPU UUID is unavailable.
func (m PCIeMeasurement) EntityKey() health.EntityKey {
	if m.entity != (health.EntityKey{}) {
		return m.entity
	}
	if m.GPUID == "" {
		return health.EntityKey{}
	}
	return health.VerifiedEntity(m.GPUID)
}

// SetEntityKey records an identity when GPUID is not a verified UUID.
func (m *PCIeMeasurement) SetEntityKey(key health.EntityKey) { m.entity = key }

// UnsupportedSignals keeps the internal sentinel out of exported observations.
func (m PCIeMeasurement) UnsupportedSignals() []string {
	var signals []string
	if m.PCIeMaxLinkWidth == UnsupportedLinkValue {
		signals = append(signals, SignalPCIeMaxLinkWidth)
	}
	if m.PCIeMaxLinkGeneration == UnsupportedLinkValue {
		signals = append(signals, SignalPCIeMaxLinkGeneration)
	}
	return signals
}
