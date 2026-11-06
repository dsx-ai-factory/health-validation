// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	maxMetricWatchFields = 127
	maxMetricWatchGroups = 64
)

// WatchPolicy configures DCGM sampling intervals for a source. The policy is
// copied during construction; changing it afterward does not reconfigure a
// running source. Intervals must be positive whole microseconds.
type WatchPolicy struct {
	// SignalIntervals overrides DefaultInterval by canonical signal ID.
	// When signals share a native field, its fastest requested interval wins.
	SignalIntervals map[string]time.Duration
	DefaultInterval time.Duration
}

func freezeWatchPolicy(policy *WatchPolicy) (*WatchPolicy, error) {
	if policy == nil {
		return nil, nil
	}
	if err := validateWatchInterval(policy.DefaultInterval); err != nil {
		return nil, fmt.Errorf("DCGM default watch interval: %w", err)
	}
	frozen := &WatchPolicy{DefaultInterval: policy.DefaultInterval}
	if policy.SignalIntervals != nil {
		frozen.SignalIntervals = make(map[string]time.Duration, len(policy.SignalIntervals))
	}
	signalIDs := make([]string, 0, len(policy.SignalIntervals))
	for signalID := range policy.SignalIntervals {
		signalIDs = append(signalIDs, signalID)
	}
	slices.Sort(signalIDs)
	for _, signalID := range signalIDs {
		if strings.TrimSpace(signalID) == "" {
			return nil, fmt.Errorf("DCGM watch interval override requires a canonical signal ID")
		}
		interval := policy.SignalIntervals[signalID]
		if err := validateWatchInterval(interval); err != nil {
			return nil, fmt.Errorf("DCGM watch interval for %q: %w", signalID, err)
		}
		frozen.SignalIntervals[signalID] = interval
	}
	return frozen, nil
}

func validateWatchInterval(interval time.Duration) error {
	if interval <= 0 || interval%time.Microsecond != 0 {
		return fmt.Errorf("interval must be positive and exactly representable in whole microseconds")
	}
	return nil
}

type metricWatchField struct {
	signalID string
	fieldID  uint16
}

type metricWatchGroup struct {
	fieldIDs []uint16
	interval time.Duration
}

// groupMetricFields uses the policy copied and validated during source construction.
func groupMetricFields(fields []metricWatchField, policy *WatchPolicy) ([]metricWatchGroup, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	if policy == nil {
		// Keep the existing watch path and its field order without an option.
		group := metricWatchGroup{fieldIDs: make([]uint16, 0, len(fields))}
		seen := make(map[uint16]struct{}, len(fields))
		for _, field := range fields {
			if _, duplicate := seen[field.fieldID]; duplicate {
				continue
			}
			seen[field.fieldID] = struct{}{}
			group.fieldIDs = append(group.fieldIDs, field.fieldID)
		}
		return []metricWatchGroup{group}, nil
	}

	intervalByField := make(map[uint16]time.Duration, len(fields))
	for _, field := range fields {
		interval := policy.DefaultInterval
		if override, exists := policy.SignalIntervals[field.signalID]; exists {
			interval = override
		}
		if previous, exists := intervalByField[field.fieldID]; !exists || interval < previous {
			intervalByField[field.fieldID] = interval
		}
	}
	fieldsByInterval := make(map[time.Duration][]uint16)
	for fieldID, interval := range intervalByField {
		fieldsByInterval[interval] = append(fieldsByInterval[interval], fieldID)
	}
	intervals := make([]time.Duration, 0, len(fieldsByInterval))
	groupCount := 0
	for interval, fieldIDs := range fieldsByInterval {
		intervals = append(intervals, interval)
		groupCount += (len(fieldIDs) + maxMetricWatchFields - 1) / maxMetricWatchFields
	}
	if groupCount > maxMetricWatchGroups {
		return nil, fmt.Errorf("DCGM metric watch requires %d field groups; maximum is %d", groupCount, maxMetricWatchGroups)
	}
	slices.Sort(intervals)
	groups := make([]metricWatchGroup, 0, groupCount)
	for _, interval := range intervals {
		fieldIDs := fieldsByInterval[interval]
		slices.Sort(fieldIDs)
		for len(fieldIDs) > 0 {
			count := min(len(fieldIDs), maxMetricWatchFields)
			groups = append(groups, metricWatchGroup{
				fieldIDs: slices.Clone(fieldIDs[:count]),
				interval: interval,
			})
			fieldIDs = fieldIDs[count:]
		}
	}
	return groups, nil
}
