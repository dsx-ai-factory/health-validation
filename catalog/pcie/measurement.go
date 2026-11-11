// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package pcie defines the rdma_topo health check and its measurements.
package pcie

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

const (
	SignalACSFailures        = "acs_failures"
	SignalIOMMUGroupFailures = "iommu_group_failures"
	SignalEvidence           = "evidence"
)

var measurementSignals = []string{
	SignalACSFailures,
	SignalIOMMUGroupFailures,
	SignalEvidence,
}

// MeasurementSignals lists every signal an RDMATopoMeasurement carries.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// Finding preserves one confirmed rdma_topo failure and any reported PCI addresses.
type Finding struct {
	Diagnostic string   `json:"diagnostic"`
	Actual     string   `json:"actual,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	BDFs       []string `json:"bdfs"`
}

// Evidence identifies the invocation that produced the findings.
type Evidence struct {
	Host        string   `json:"host"`
	Executable  string   `json:"executable"`
	ToolVersion string   `json:"tool_version"`
	Mode        string   `json:"mode"`
	Invocation  []string `json:"invocation"`
	ExitCode    int      `json:"exit_code"`
}

// RDMATopoMeasurement contains ACS and IOMMU group findings from rdma_topo check.
// Collection failures use a separate measurement so they cannot hide findings.
type RDMATopoMeasurement struct {
	ACSFailures        []Finding `json:"acs_failures"`
	IOMMUGroupFailures []Finding `json:"iommu_group_failures"`
	errors             []health.ErroredMeasurement
	Evidence           Evidence `json:"evidence"`
}

// ErroredRDMATopo returns a measurement marked errored for the failed signals.
func ErroredRDMATopo(err error, failedSignals ...string) RDMATopoMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	var m RDMATopoMeasurement
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection failure to the measurement.
func (m *RDMATopoMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (RDMATopoMeasurement) Category() health.Category { return health.PCIe }

// Errors implements [health.Measurement].
func (m RDMATopoMeasurement) Errors() []health.ErroredMeasurement { return m.errors }

// EntityKey keeps node findings distinct from source-level collection failures.
func (m RDMATopoMeasurement) EntityKey() health.EntityKey {
	if len(m.ACSFailures)+len(m.IOMMUGroupFailures) > 0 {
		return health.NodeEntity()
	}
	for _, signal := range measurementSignals {
		if !slices.ContainsFunc(m.errors, func(err health.ErroredMeasurement) bool {
			return slices.Contains(err.FailedSignals(), signal)
		}) {
			return health.NodeEntity()
		}
	}
	return health.SourceEntity("rdma_topo")
}
