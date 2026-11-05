// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
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
				add(input.Category(), evaluation.SchemaKeys(input)...)
			}
		}
		if !derived {
			add(cd.Category, signal)
		}
	}
	return out
}

// producesSignal reports whether one of a's outputs in category carries
// signal.
func producesSignal(a health.Accumulator, category health.Category, signal string) bool {
	for _, out := range a.Outputs() {
		if out.Category() == category && slices.Contains(evaluation.SchemaKeys(out), signal) {
			return true
		}
	}
	return false
}
