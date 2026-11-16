// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"maps"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
)

// Override is an operator-supplied threshold value that replaces
// the catalog default for a specific check. Overrides for Fixed
// thresholds are rejected at evaluation time.
type Override struct {
	Value         any    `json:"value"`
	CheckID       string `json:"check_id"`
	ThresholdName string `json:"threshold_name"`
}

// OverrideSet holds a collection of operator overrides indexed for
// fast lookup during evaluation.
type OverrideSet struct {
	// overrides is keyed by checkID, then by threshold name.
	overrides map[string]map[string]any
}

// NewOverrideSet creates an OverrideSet from a list of overrides.
func NewOverrideSet(overrides ...Override) OverrideSet {
	os := OverrideSet{
		overrides: make(map[string]map[string]any),
	}
	for _, o := range overrides {
		if os.overrides[o.CheckID] == nil {
			os.overrides[o.CheckID] = make(map[string]any)
		}
		os.overrides[o.CheckID][o.ThresholdName] = o.Value
	}
	return os
}

// ForCheck returns a copy of the threshold overrides for a specific check.
// Returns nil if no overrides exist for the check.
func (os *OverrideSet) ForCheck(checkID string) map[string]any {
	if os.overrides == nil {
		return nil
	}
	out := maps.Clone(os.overrides[checkID])
	for name, value := range out {
		out[name] = cloneThresholdValue(value)
	}
	return out
}

// ConfiguredOverride is an operator override as evaluation would apply it:
// Value is the coerced override and OriginalValue the catalog value it replaces
// for the catalog's architecture.
type ConfiguredOverride struct {
	Value         any    `json:"value"`
	OriginalValue any    `json:"original_value"`
	CheckID       string `json:"check_id"`
	ThresholdName string `json:"threshold_name"`
}

// ConfiguredOverrides returns every override that targets a registered check's
// threshold, disabled checks included, resolved the way evaluation resolves
// it and sorted by check ID, then threshold name. It returns the error
// evaluation would for an override it rejects, such as one on a Fixed
// threshold.
func (c *Catalog) ConfiguredOverrides() ([]ConfiguredOverride, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var configured []ConfiguredOverride
	for _, checkID := range slices.Sorted(maps.Keys(c.overrides.overrides)) {
		cd, ok := c.checks[checkID]
		if !ok {
			continue
		}
		set := c.overrides.overrides[checkID]
		resolved, err := evaluation.ResolveThresholds(cd.ID, cd.Thresholds, set, c.architecture)
		if err != nil {
			return nil, err
		}
		for _, name := range slices.Sorted(maps.Keys(set)) {
			rt, ok := resolved[name]
			if !ok {
				continue
			}
			configured = append(configured, ConfiguredOverride{
				Value:         cloneThresholdValue(rt.Value),
				OriginalValue: cloneThresholdValue(rt.OriginalValue),
				CheckID:       checkID,
				ThresholdName: name,
			})
		}
	}
	return configured, nil
}
