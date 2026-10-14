// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"maps"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// cloneDefinition copies every slice and map a definition owns so the
// registry and its callers never share mutable state. Threshold values are
// scalars and are shared.
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
		spec.PerArchitecture = maps.Clone(spec.PerArchitecture)
		out[name] = spec
	}
	return out
}

func cloneAction(a health.RecommendedAction) health.RecommendedAction {
	a.RemediationActions = slices.Clone(a.RemediationActions)
	return a
}
