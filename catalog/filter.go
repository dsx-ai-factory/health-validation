// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Filter selects a subset of check definitions from the catalog.
// Zero-value fields are ignored (match all). Multiple non-zero fields
// are combined with AND.
type Filter struct {
	Category      *health.Category
	Provenance    *health.Provenance
	Cadence       *health.SchedulingCadence
	MaturityLevel *health.MaturityLevel
	Architecture  string
}

func (f Filter) matches(cd *health.CheckDefinition) bool {
	if f.Architecture != "" && len(cd.Architectures) > 0 {
		if !slices.Contains(cd.Architectures, f.Architecture) {
			return false
		}
	}
	if f.Category != nil && cd.Category != *f.Category {
		return false
	}
	if f.Provenance != nil && cd.Provenance != *f.Provenance {
		return false
	}
	// Cadences nest by frequency (Continuous > Prolog > MaintenanceWindow):
	// a check matches when its own cadence covers the requested one, so
	// filtering to Prolog also returns Continuous checks, and filtering to
	// MaintenanceWindow returns every check.
	if f.Cadence != nil && !cd.SchedulingCadence.Covers(*f.Cadence) {
		return false
	}
	if f.MaturityLevel != nil && cd.MaturityLevel != *f.MaturityLevel {
		return false
	}
	return true
}
