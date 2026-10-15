// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package catalogtest provides helpers for tests that evaluate a Catalog.
package catalogtest

import (
	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// DummyArchitecture is a non-empty architecture identifier for tests that
// must call Evaluate but do not exercise architecture filtering or
// per-architecture thresholds.
const DummyArchitecture = "dummyArch"

// CheckIDsAtCadence returns the IDs of checks whose declared cadence equals
// cadence. List's Cadence filter is the nested run window, so filtering to
// Prolog also returns Continuous checks.
func CheckIDsAtCadence(cat *catalog.Catalog, cadence health.SchedulingCadence) []string {
	var ids []string
	for _, cd := range cat.List(catalog.Filter{}) {
		if cd.SchedulingCadence == cadence {
			ids = append(ids, cd.ID)
		}
	}
	return ids
}
