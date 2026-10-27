// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"maps"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// cloneDefinition copies every slice and map a definition owns so the
// registry and its callers never share mutable state.
func cloneDefinition(cd health.CheckDefinition) health.CheckDefinition {
	cd.Thresholds = cloneThresholds(cd.Thresholds)
	cd.Dependencies = slices.Clone(cd.Dependencies)
	cd.Architectures = slices.Clone(cd.Architectures)
	cd.ExecutionContexts = slices.Clone(cd.ExecutionContexts)
	cd.Prerequisites = slices.Clone(cd.Prerequisites)
	cd.RequiredSignals = slices.Clone(cd.RequiredSignals)
	cd.RecommendedAction = cloneAction(cd.RecommendedAction)
	if cd.Rules != nil {
		rules := make([]health.Rule, len(cd.Rules))
		for i, r := range cd.Rules {
			r.RecommendedAction = cloneAction(r.RecommendedAction)
			rules[i] = r
		}
		cd.Rules = rules
	}
	return cd
}

func cloneThresholds(in map[string]health.ThresholdSpec) map[string]health.ThresholdSpec {
	if in == nil {
		return nil
	}
	out := make(map[string]health.ThresholdSpec, len(in))
	for name, spec := range in {
		spec.Value = cloneThresholdValue(spec.Value)
		spec.PerArchitecture = maps.Clone(spec.PerArchitecture)
		for arch, value := range spec.PerArchitecture {
			spec.PerArchitecture[arch] = cloneThresholdValue(value)
		}
		out[name] = spec
	}
	return out
}

// cloneThresholdValue copies a string-list value, the one threshold type
// that is not a scalar: a ThresholdSpec copy shares the backing array of a
// slice held in an any field. The bounds need no copy, being numeric only.
func cloneThresholdValue(v any) any {
	if list, ok := v.([]string); ok {
		return slices.Clone(list)
	}
	return v
}

func cloneAction(a health.RecommendedAction) health.RecommendedAction {
	a.RemediationActions = slices.Clone(a.RemediationActions)
	return a
}
