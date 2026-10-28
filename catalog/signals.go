// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
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
		typeName := measurementTypeName(m)
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

// measurementTypeName names m's struct type the same way whether m is
// passed by value or by pointer, so a type is never reported as
// colliding with itself.
func measurementTypeName(m health.Measurement) string {
	t := reflect.TypeOf(m)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.String()
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
	// Registered accumulators' outputs are measurement types the catalog
	// produces itself, so a check reading only derived signals is covered
	// without the caller naming them.
	measurements = slices.Clone(measurements)
	c.mu.RLock()
	for _, a := range c.accumulators {
		measurements = append(measurements, a.Outputs()...)
	}
	c.mu.RUnlock()

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
