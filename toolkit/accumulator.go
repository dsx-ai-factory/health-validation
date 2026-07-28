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

package toolkit

import "github.com/NVIDIA/health-validation/catalog/health"

// Accumulator transforms measurements into derived measurements.
//
// Use cases include rolling averages, error rate calculation,
// sustained-value detection, and multi-sample aggregation.
// Accumulators are optional in the pipeline; many deployments
// feed sensor measurements directly to checks.
type Accumulator interface {
	// Name identifies this accumulator.
	Name() string

	// Accumulate processes an input measurement and optionally
	// produces derived measurements. Returns nil if no output
	// is ready yet (e.g., still collecting samples for a window).
	Accumulate(m health.Measurement) ([]health.Measurement, error)
}
