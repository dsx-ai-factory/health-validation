package catalog

import (
	"slices"

	"github.com/NVIDIA/health-validation/catalog/health"
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
	if f.Cadence != nil && cd.SchedulingCadence != *f.Cadence {
		return false
	}
	if f.MaturityLevel != nil && cd.MaturityLevel != *f.MaturityLevel {
		return false
	}
	return true
}
