package catalog

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/internal/evaluation"
)

// CheckSignalUniqueness verifies that within each Category no signal name
// is carried by more than one measurement type. This is the invariant
// that makes signal-subset routing unambiguous: a check's RequiredSignals
// then identify exactly one measurement type per category, so evaluation
// never routes a check to a measurement it was not written for.
//
// Pass one instance — the zero value is fine — of every measurement type
// the catalog consumes, both the built-ins and any a consumer adds. It
// returns an error naming the first collision found. Consumers that
// extend the catalog with their own measurement types should assert on it
// in a test so the invariant is enforced at build time.
func CheckSignalUniqueness(measurements ...health.Measurement) error {
	owners := make(map[health.Category]map[string]string)
	for _, m := range measurements {
		cat := m.Category()
		typeName := fmt.Sprintf("%T", m)
		byCategory := owners[cat]
		if byCategory == nil {
			byCategory = make(map[string]string)
			owners[cat] = byCategory
		}
		for _, sig := range evaluation.SchemaKeys(m) {
			if prev, ok := byCategory[sig]; ok && prev != typeName {
				return fmt.Errorf(
					"signal %q in category %s is carried by both %s and %s; signal names must be "+
						"unique within a category so a check routes to exactly one measurement type",
					sig, cat, prev, typeName)
			}
			byCategory[sig] = typeName
		}
	}
	return nil
}

// measurementSchema is a measurement type's routing surface: its category
// and the set of signal names its struct schema carries.
type measurementSchema struct {
	keys     map[string]struct{}
	category health.Category
}

// CheckCoverage verifies that every registered check is feedable: at
// least one of the provided measurement types shares the check's Category
// and carries all of its RequiredSignals. A check with no such provider
// can never be routed a live measurement, so it can never yield a real
// verdict — at runtime it is a perpetual NoApplicableMeasurement skip
// (when its category is otherwise collected) or no verdict at all (when
// its category is never collected). Either way it is dead weight, so this
// turns that latent authoring gap (a renamed signal, or a check
// registered without the measurement type that feeds it) into an error at
// test time.
//
// Pass one instance — the zero value is fine — of every measurement type
// the catalog's sensors produce. It returns an error naming the first
// uncovered check, in ID order for a stable message. Consumers typically
// assert on it in a test alongside [CheckSignalUniqueness].
func (c *Catalog) CheckCoverage(measurements ...health.Measurement) error {
	schemas := make([]measurementSchema, 0, len(measurements))
	for _, m := range measurements {
		keys := make(map[string]struct{})
		for _, k := range evaluation.SchemaKeys(m) {
			keys[k] = struct{}{}
		}
		schemas = append(schemas, measurementSchema{keys: keys, category: m.Category()})
	}

	c.mu.RLock()
	checks := make([]*health.CheckDefinition, 0, len(c.checks))
	for _, cd := range c.checks {
		checks = append(checks, cd)
	}
	c.mu.RUnlock()
	slices.SortFunc(checks, func(a, b *health.CheckDefinition) int { return cmp.Compare(a.ID, b.ID) })

	for _, cd := range checks {
		if !coveredBy(cd, schemas) {
			return fmt.Errorf(
				"check %q requires signals %v in category %s, but no supplied measurement type carries them",
				cd.ID, cd.RequiredSignals, cd.Category)
		}
	}
	return nil
}

// coveredBy reports whether some schema shares the check's category and
// carries all of its RequiredSignals — the schema-level form of the
// routing predicate the evaluation engine applies to a live measurement.
func coveredBy(cd *health.CheckDefinition, schemas []measurementSchema) bool {
	for _, s := range schemas {
		if s.category != cd.Category {
			continue
		}
		hasAll := true
		for _, sig := range cd.RequiredSignals {
			if _, ok := s.keys[sig]; !ok {
				hasAll = false
				break
			}
		}
		if hasAll {
			return true
		}
	}
	return false
}
