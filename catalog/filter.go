/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package catalog

import (
	"slices"

	"github.com/NVIDIA/health-validation/catalog/health"
)

// Filter selects a subset of check definitions from the catalog.
// Zero-value fields are ignored (match all). Multiple non-zero fields
// are combined with AND.
type Filter struct {
	Architecture  string
	Category      *health.Category
	Provenance    *health.Provenance
	Cadence       *health.SchedulingCadence
	MaturityLevel string
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
	if f.MaturityLevel != "" && cd.MaturityLevel != f.MaturityLevel {
		return false
	}
	return true
}
