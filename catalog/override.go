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

// Override is an operator-supplied threshold value that replaces
// the catalog default for a specific check. Overrides for Fixed
// thresholds are rejected at evaluation time.
type Override struct {
	Value         any    `json:"value"`
	CheckID       string `json:"check_id"`
	ThresholdName string `json:"threshold_name"`
}

// OverrideSet holds a collection of operator overrides indexed for
// fast lookup during evaluation.
type OverrideSet struct {
	// overrides is keyed by checkID, then by threshold name.
	overrides map[string]map[string]any
}

// NewOverrideSet creates an OverrideSet from a list of overrides.
func NewOverrideSet(overrides ...Override) OverrideSet {
	os := OverrideSet{
		overrides: make(map[string]map[string]any),
	}
	for _, o := range overrides {
		if os.overrides[o.CheckID] == nil {
			os.overrides[o.CheckID] = make(map[string]any)
		}
		os.overrides[o.CheckID][o.ThresholdName] = o.Value
	}
	return os
}

// ForCheck returns the threshold overrides for a specific check.
// Returns nil if no overrides exist for the check.
func (os *OverrideSet) ForCheck(checkID string) map[string]any {
	if os.overrides == nil {
		return nil
	}
	return os.overrides[checkID]
}
