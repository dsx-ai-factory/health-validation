// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"reflect"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/evaluation"
)

// CollectedSignals returns, by category, the signals a collector must
// gather for cd. A required signal a registered accumulator derives is
// replaced by the signals of that accumulator's inputs, since only those
// can be collected; any other required signal stands as it is.
func (c *Catalog) CollectedSignals(cd *health.CheckDefinition) map[health.Category][]string {
	c.mu.RLock()
	accumulators := slices.Clone(c.accumulators)
	c.mu.RUnlock()

	out := make(map[health.Category][]string)
	add := func(category health.Category, signals ...string) {
		for _, s := range signals {
			if !slices.Contains(out[category], s) {
				out[category] = append(out[category], s)
			}
		}
	}
	for _, signal := range cd.RequiredSignals {
		derived := false
		for _, a := range accumulators {
			if !producesSignal(a, cd.Category, signal) {
				continue
			}
			derived = true
			for _, input := range a.Inputs() {
				// A refined input's schema carries the derived signals
				// too, which no collector can supply.
				for _, k := range evaluation.SchemaKeys(input) {
					if !slices.Contains(a.Derived(), k) {
						add(input.Category(), k)
					}
				}
			}
		}
		if !derived {
			add(cd.Category, signal)
		}
	}
	return out
}

// producesSignal reports whether a derives signal in category.
func producesSignal(a health.Accumulator, category health.Category, signal string) bool {
	if !slices.Contains(a.Derived(), signal) {
		return false
	}
	for _, out := range a.Outputs() {
		if out.Category() == category && slices.Contains(evaluation.SchemaKeys(out), signal) {
			return true
		}
	}
	return false
}

// refinedTypes returns the measurement types a refines: its outputs whose
// type is also one of its inputs.
func refinedTypes(a health.Accumulator) []reflect.Type {
	var refined []reflect.Type
	for _, out := range a.Outputs() {
		t := reflect.TypeOf(out)
		for _, in := range a.Inputs() {
			if reflect.TypeOf(in) == t {
				refined = append(refined, t)
				break
			}
		}
	}
	return refined
}
