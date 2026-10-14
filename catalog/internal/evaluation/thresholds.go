// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package evaluation

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// resolveThresholds merges catalog-authored thresholds with operator
// overrides for the given architecture. A threshold's PerArchitecture entry
// for architecture becomes its base value (else Value); an operator override
// then applies on top. Returns an error if an override targets a Fixed
// threshold.
func resolveThresholds(checkID string, specs map[string]health.ThresholdSpec, overrides map[string]any, architecture string) (map[string]health.ResolvedThreshold, error) {
	resolved := make(map[string]health.ResolvedThreshold, len(specs))

	for name, spec := range specs {
		base := spec.Value
		if archValue, ok := spec.PerArchitecture[architecture]; ok {
			base = archValue
		}

		rt := health.ResolvedThreshold{
			Value:         base,
			Category:      spec.Category,
			OriginalValue: base,
		}

		if override, ok := overrides[name]; ok {
			if spec.Category == health.FixedThreshold {
				return nil, &health.FixedThresholdError{
					ThresholdName: name,
					CheckID:       checkID,
				}
			}
			rt.Value = override
			rt.Overridden = true
		}

		resolved[name] = rt
	}

	return resolved, nil
}
