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

package health

import "time"

// CheckDefinition is the canonical shape of a health check in the catalog.
// Each definition encodes NVIDIA's operational judgment about a specific
// hardware or driver condition: which signals to examine, what thresholds
// apply, and what action to recommend.
//
// Check definitions are data. They carry no sensor or hardware dependencies.
// Evaluation happens when a caller provides signal values to [Catalog.Evaluate].
type CheckDefinition struct {
	Thresholds        map[string]ThresholdSpec `json:"thresholds"`
	MaturityLevel     string                   `json:"maturity_level"`
	FeasibilityClass  string                   `json:"feasibility_class,omitempty"`
	Description       string                   `json:"description"`
	Name              string                   `json:"name"`
	ID                string                   `json:"id"`
	RequiredSignals   []string                 `json:"required_signals"`
	Dependencies      []string                 `json:"dependencies,omitempty"`
	Rules             []Rule                   `json:"rules"`
	Architectures     []string                 `json:"architectures,omitempty"`
	ExecutionContexts []ExecutionContext       `json:"execution_contexts"`
	Prerequisites     []Prerequisite           `json:"prerequisites,omitempty"`
	Timeout           time.Duration            `json:"timeout,omitempty"`
	SchedulingCadence SchedulingCadence        `json:"scheduling_cadence"`
	Category          Category                 `json:"category"`
	Provenance        Provenance               `json:"provenance"`
	ExecutionClass    ExecutionClass           `json:"execution_class"`
	RecommendedAction RecommendedAction        `json:"recommended_action,omitzero"`
}

// Rule defines a single evaluation condition within a check. Rules are
// evaluated in declaration order; the first rule whose CEL expression
// evaluates to true determines the verdict.
type Rule struct {
	// Expression is a CEL expression that evaluates to a boolean.
	// Variables available are the union of signal field names and
	// threshold names from the parent CheckDefinition.
	Expression string `json:"expression"`

	// Message describes the detected condition. May contain ${variable}
	// placeholders interpolated from signal and threshold values.
	Message string `json:"message"`

	// Severity produced when this rule matches.
	Severity Severity `json:"severity"`

	// RecommendedAction, when non-zero (see
	// [RecommendedAction.IsZero]), overrides the parent
	// [CheckDefinition.RecommendedAction] in the resulting
	// [CheckVerdict]. Use it when a specific failure mode warrants a
	// different remediation than the check's default. A zero value
	// leaves the check-level action in place.
	RecommendedAction RecommendedAction `json:"recommended_action,omitzero"`
}

// HasRecommendedAction reports whether the rule sets a non-zero
// RecommendedAction that should override the parent
// [CheckDefinition.RecommendedAction].
func (r Rule) HasRecommendedAction() bool {
	return !r.RecommendedAction.IsZero()
}

// Prerequisite declares a minimum software requirement for a check.
type Prerequisite struct {
	Name       string `json:"name"`
	MinVersion string `json:"min_version"`
}
