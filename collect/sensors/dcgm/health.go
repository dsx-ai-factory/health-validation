// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"github.com/dsx-ai-factory/health-validation/collect/signals/dcgmhealth"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

type healthSource interface {
	// ReadHealth returns a nonnil report on success, or nil and a collection error.
	// The source validates missing native reports; success must never be nil, nil.
	ReadHealth(context.Context, *collect.Cycle) (*observation.DCGMHealthReport, *observation.CollectionError)
}

type configurableSource interface {
	metricSource
	healthSource
	AddHealthToWatch(uint64) error
}

// NewWithHealth creates a sensor with optional passive DCGM health and metrics.
// A nonzero systems mask includes dcgmhealth.Report automatically. A zero mask
// disables health registration, output, and reads, even when another sensor has
// registered health on the same source. Report IDs are not metric inputs.
//
// metricSignalIDs selects only explicitly requested metrics. With no metric IDs,
// a nonzero systems mask collects health only, while zero selects no outputs.
// Optional inventory collection remains available in either case.
//
// The systems mask configures health independently of metricSignalIDs and may
// contain only dcgmhealth.KnownSystems bits. Systems without local targets are
// valid requests; their availability is determined during collection.
// Combine flags such as dcgmhealth.SystemMemory|dcgmhealth.SystemPCIe to request
// multiple subsystems. Health setup occurs lazily on the first health read.
//
// Construct all sensors sharing source before its first metric or health read.
// Inventory reads leave registration open. The source unions registered health
// masks; per-call signal selection does not reconfigure the watch.
func NewWithHealth(source *dcgmsource.Source, systems uint64, metricSignalIDs ...string) (collect.Sensor, error) {
	if source == nil {
		return nil, fmt.Errorf("DCGM source is required")
	}
	return newHealthSensor(source, systems, metricSignalIDs)
}

// newHealthSensor validates metric IDs and registers optional health before metrics.
// A zero mask skips health; invalid nonzero masks leave no metric registrations.
func newHealthSensor(source configurableSource, systems uint64, metricSignalIDs []string) (collect.Sensor, error) {
	var metrics collect.Signals
	if len(metricSignalIDs) > 0 {
		var err error
		metrics, err = resolveMetricSignals(metricSignalIDs)
		if err != nil {
			return nil, err
		}
	}
	if systems != 0 {
		if err := source.AddHealthToWatch(systems); err != nil {
			return nil, fmt.Errorf("configure DCGM health watch: %w", err)
		}
	}
	result, err := newSensor(source, metrics)
	if err != nil {
		return nil, err
	}
	if systems != 0 {
		result.health = source
		result.signals = append(result.signals, dcgmhealth.Report)
	}
	return result, nil
}

// splitSignals separates metric definitions from selection of the aggregate report.
func splitSignals(definitions collect.Signals) (metrics collect.Signals, health bool) {
	for _, definition := range definitions {
		if definition.ID() == dcgmhealth.Report.ID() {
			health = true
		} else {
			metrics = append(metrics, definition)
		}
	}
	return metrics, health
}

// appendHealth adds one node-wide report, collection error, or no-target outcome.
// Reports are cloned, and incidents are preserved even when targets are absent.
func (sensor *sensor) appendHealth(
	ctx context.Context,
	cycle *collect.Cycle,
	batch *observation.ObservationBatch,
	observedAt time.Time,
) {
	definition := dcgmhealth.Report
	if sensor.health == nil {
		sensor.appendError(batch, definition, observation.NodeTarget(""), observedAt, "health",
			&observation.CollectionError{
				Category: observation.CollectionErrorCategoryNotConfigured,
				Detail:   "DCGM health source is not configured",
			})
		return
	}
	report, failure := sensor.health.ReadHealth(ctx, cycle)
	if failure != nil {
		sensor.appendError(batch, definition, observation.NodeTarget(""), observedAt, "health", failure)
		return
	}
	if len(report.GetIncidents()) == 0 && report.GetRequestedSystems() != 0 && report.GetSystemsWithoutTargets() == report.GetRequestedSystems() {
		sensor.appendHealthNoValue(batch, definition, observedAt, observation.NoValueReasonNoTargets,
			"No targets detected for the requested DCGM health systems")
		return
	}
	batch.Observations = append(batch.Observations, &observation.Observation{
		SignalId:   definition.ID(),
		Source:     sensor.source.ID(),
		Target:     observation.NodeTarget(""),
		ObservedAt: timestamppb.New(observedAt),
		Unit:       definition.Unit(),
		Outcome: &observation.Observation_Value{Value: &observation.Value{
			Kind: &observation.Value_DCGMHealthReport{DcgmHealthReport: proto.Clone(report).(*observation.DCGMHealthReport)},
		}},
	})
}

// appendHealthNoValue records source-wide health unavailability.
func (sensor *sensor) appendHealthNoValue(
	batch *observation.ObservationBatch,
	definition collect.Signal,
	observedAt time.Time,
	reason observation.NoValueReason,
	detail string,
) {
	batch.Observations = append(batch.Observations, observation.NewNoValueObservation(
		definition.ID(), definition.Unit(), sensor.source.ID(), nil,
		timestamppb.New(observedAt), &observation.NoValue{Reason: reason, Detail: detail},
	))
}
