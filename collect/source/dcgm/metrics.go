// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// FieldValue is one DCGM field returned by a GPU read. The sensor adds the
// signal metadata, target, and source before emitting an observation.
type FieldValue struct {
	ObservedAt      time.Time
	Value           *observation.Value
	NoValue         *observation.NoValue
	CollectionError *observation.CollectionError
}

// ReadGPUMetrics reads the requested fields for one GPU in a single DCGM call.
// It returns field-level failures alongside successful fields so the sensor
// can emit every available observation from the GPU.
func (source *Source) ReadGPUMetrics(
	ctx context.Context,
	currentCycle *collect.Cycle,
	gpuIndex uint,
	signalIDs []string,
) map[string]FieldValue {
	// The first read fixes the field watch. Report signals outside that watch
	// individually instead of preventing collection of registered signals.
	source.metricWatchFrozen.Store(true)
	values := make(map[string]FieldValue, len(signalIDs))
	watched := make([]string, 0, len(signalIDs))
	for _, signalID := range signalIDs {
		if slices.Contains(source.metricWatchSignals, signalID) {
			watched = append(watched, signalID)
			continue
		}
		failure := observation.CollectionError{
			Source:   Name,
			Category: observation.CollectionErrorCategoryNotConfigured,
			Detail:   fmt.Sprintf("DCGM signal %q is not in the metric watch", signalID),
		}
		values[signalID] = FieldValue{CollectionError: &failure}
	}
	if len(watched) == 0 {
		return values
	}

	collected, err := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) (map[string]FieldValue, error) {
		if err := source.startNativeCall(); err != nil {
			return nil, err
		}
		defer source.finishNativeCall()

		if err := source.ensureInitialized(); err != nil {
			return nil, err
		}
		if err := source.ensureMetricWatch(); err != nil {
			return nil, err
		}
		if err := callContext.Err(); err != nil {
			return nil, err
		}

		return source.native.ReadGPUMetrics(gpuIndex, watched)
	})
	if err != nil {
		// One failure message is shared by every watched signal: the content
		// is identical and observations treat it as read-only.
		failure := collectionError(err)
		for _, signalID := range watched {
			values[signalID] = FieldValue{CollectionError: &failure}
		}
		return values
	}
	for signalID, value := range collected {
		values[signalID] = value
	}
	return values
}
