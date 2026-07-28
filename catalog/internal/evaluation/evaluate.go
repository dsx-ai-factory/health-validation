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

// Package evaluation implements the health check evaluation pipeline.
//
// It evaluates check definitions against signal values using CEL
// expressions, resolves threshold overrides, and aggregates per-check
// verdicts into a node-level health assessment.
package evaluation

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/internal/celeval"
)

// EvaluationEngine evaluates health check definitions against signal values.
// It is safe for concurrent use.
type EvaluationEngine struct {
	cel *celeval.Evaluator
}

// NewEvaluationEngine creates an evaluation engine.
func NewEvaluationEngine() *EvaluationEngine {
	return &EvaluationEngine{cel: celeval.New()}
}

// Params holds the evaluation context that is independent of any
// particular measurement: how the host is configured, which thresholds
// the operator has overridden, whether checks have been disabled.
// Per-measurement state (signal values, the errored-entity placeholder)
// is supplied via the Measurement passed to EvaluateCheck and
// EvaluateAllRaw, not through this struct.
type Params struct {
	Overrides        map[string]any
	Architecture     string
	ExecutionContext health.ExecutionContext
	Disabled         bool
}

// EvaluateCheck runs a single check against a measurement.
//
// When the measurement reports any Errors, the engine inspects each
// entry's FailedSignals: a check whose RequiredSignals overlap with
// any error short-circuits to a Critical verdict joining the
// matching errors' details; a check whose RequiredSignals were all
// collected still runs CEL against the populated fields. Checks
// whose RequiredSignals fall outside the measurement schema continue
// to skip with the existing "missing required signal" reason.
func (e *EvaluationEngine) EvaluateCheck(cd *health.CheckDefinition, m health.Measurement, p Params) (health.CheckVerdict, error) {
	return e.evaluateCheck(cd, ToSignals(m), m.Errors(), p)
}

// EvaluateAll runs all provided checks against a measurement,
// respecting dependency ordering, and aggregates into a node-level
// verdict.
func (e *EvaluationEngine) EvaluateAll(checks map[string]*health.CheckDefinition, m health.Measurement, paramsFor func(checkID string) Params, completionThreshold float64) (health.NodeVerdict, error) {
	verdicts, err := e.EvaluateAllRaw(checks, m, paramsFor)
	if err != nil {
		return health.NodeVerdict{}, err
	}
	return health.AggregateNodeVerdict(verdicts, completionThreshold), nil
}

// EvaluateAllRaw runs all checks against a single measurement and
// returns the flat verdict list before aggregation. The measurement's
// signals and errored-entity status are extracted once and shared
// across every check.
//
// paramsFor supplies the evaluation context for a given check ID.
// Architecture and execution context are host-wide, but overrides and
// disabled state vary per check, so the engine asks for fresh Params for
// each check rather than reusing one set across the batch. Callers build
// these Params the same way they do for the single-check EvaluateCheck,
// keeping the two paths in lockstep.
func (e *EvaluationEngine) EvaluateAllRaw(checks map[string]*health.CheckDefinition, m health.Measurement, paramsFor func(checkID string) Params) ([]health.CheckVerdict, error) {
	signals := ToSignals(m)
	errors := m.Errors()

	order := TopologicalOrder(checks)
	verdicts := make([]health.CheckVerdict, 0, len(order))
	results := make(map[string]*health.CheckVerdict)

	for _, id := range order {
		cd := checks[id]

		depSkipped := false
		for _, dep := range cd.Dependencies {
			if prev, ok := results[dep]; ok && prev.Skipped {
				v := skippedVerdict(cd, time.Now(), health.DependencySkipped,
					fmt.Sprintf("dependency %q was skipped", dep))
				verdicts = append(verdicts, v)
				results[id] = &verdicts[len(verdicts)-1]
				depSkipped = true
				break
			}
		}
		if depSkipped {
			continue
		}

		v, err := e.evaluateCheck(cd, signals, errors, paramsFor(id))
		if err != nil {
			return nil, fmt.Errorf("evaluating %q: %w", id, err)
		}
		verdicts = append(verdicts, v)
		results[id] = &verdicts[len(verdicts)-1]
	}

	return verdicts, nil
}

// evaluateCheck runs a single check against pre-extracted signals plus
// the measurement's accumulated error list (nil/empty when the
// measurement is fully observable). Used by EvaluateCheck and
// EvaluateAllRaw so the latter can avoid re-deriving signals for
// every check in the loop.
func (e *EvaluationEngine) evaluateCheck(cd *health.CheckDefinition, signals map[string]any, errors []health.ErroredMeasurement, p Params) (health.CheckVerdict, error) {
	now := time.Now()

	if p.Disabled {
		return skippedVerdict(cd, now, health.CheckDisabled, "disabled by operator configuration"), nil
	}

	if p.Architecture != "" && len(cd.Architectures) > 0 && !slices.Contains(cd.Architectures, p.Architecture) {
		return skippedVerdict(cd, now, health.ArchitectureUnsupported,
			fmt.Sprintf("check requires %v, running on %s", cd.Architectures, p.Architecture)), nil
	}

	if len(cd.ExecutionContexts) > 0 && !slices.Contains(cd.ExecutionContexts, p.ExecutionContext) {
		return skippedVerdict(cd, now, health.DeploymentUnsupported,
			fmt.Sprintf("check requires %v, running in %s", cd.ExecutionContexts, p.ExecutionContext)), nil
	}

	for _, sig := range cd.RequiredSignals {
		if _, ok := signals[sig]; !ok {
			return skippedVerdict(cd, now, health.DeploymentUnsupported,
				fmt.Sprintf("missing required signal %q", sig)), nil
		}
	}

	// If any of the sensor's per-field failures cover a signal the
	// check requires, fail the check with a Critical verdict rather
	// than running CEL against a zero value where a real measurement
	// should be. The verdict's detail joins every error entry that
	// touched one of this check's required signals so the message
	// names exactly what went wrong. Checks whose required signals
	// were all collected fall through to normal evaluation against
	// the partial data.
	if detail, action := matchingError(errors, cd.RequiredSignals); detail != "" {
		// Observation failure: the data was unavailable, so the rules never
		// ran. The verdict carries a collection-failure action, never the
		// check's hardware-remediation default (that is for matched rules).
		return ErroredVerdict(cd, now, detail, action), nil
	}

	resolved, err := health.ResolveThresholds(cd.ID, cd.Thresholds, p.Overrides, p.Architecture)
	if err != nil {
		return health.CheckVerdict{}, err
	}

	// Merge signals and resolved threshold values into a flat map
	// for CEL evaluation.
	vars := make(map[string]any, len(signals)+len(resolved))
	for k, v := range signals {
		vars[k] = v
	}
	for k, rt := range resolved {
		vars[k] = rt.Value
	}

	// Evaluate every rule and aggregate the matches. Severity comes from
	// the worst (largest) value across matching rules; Messages preserves
	// declaration order so consumers see the rules' findings in the order
	// they were authored. RecommendedAction comes from the
	// highest-priority matching rule (rank: severity desc, then
	// declaration order asc) — its override if non-empty, else the check
	// default.
	var (
		messages         []string
		severity         = health.Pass
		topSeverity      health.Severity
		topRuleAction    health.RecommendedAction
		topRuleHasAction bool
		anyMatched       bool
	)

	for _, rule := range cd.Rules {
		matched, err := e.cel.Eval(rule.Expression, vars)
		if err != nil {
			return health.CheckVerdict{}, fmt.Errorf("check %q rule %q: %w", cd.ID, rule.Expression, err)
		}
		if !matched {
			continue
		}

		messages = append(messages, interpolateMessage(rule.Message, vars))
		severity = health.Worse(severity, rule.Severity)

		if !anyMatched || rule.Severity > topSeverity {
			topSeverity = rule.Severity
			topRuleAction = rule.RecommendedAction
			topRuleHasAction = rule.HasRecommendedAction()
		}
		anyMatched = true
	}

	action := cd.RecommendedAction
	if topRuleHasAction {
		action = topRuleAction
	}

	if !anyMatched {
		// No rule matched; check passes.
		return health.CheckVerdict{
			CheckID:        cd.ID,
			Severity:       health.Pass,
			Category:       cd.Category,
			Thresholds:     resolved,
			ObservedValues: signals,
			MaturityLevel:  cd.MaturityLevel,
			NVIDIAEndorsed: cd.Provenance == health.NVIDIA,
			Timestamp:      now,
		}, nil
	}

	return health.CheckVerdict{
		CheckID:           cd.ID,
		Severity:          severity,
		Messages:          messages,
		Category:          cd.Category,
		Thresholds:        resolved,
		ObservedValues:    signals,
		RecommendedAction: action,
		MaturityLevel:     cd.MaturityLevel,
		NVIDIAEndorsed:    cd.Provenance == health.NVIDIA,
		Timestamp:         now,
	}, nil
}

// matchingError inspects the error entries whose FailedSignals overlap the
// check's required signals. It returns a joined detail string of their
// causes ("" when none match) and the recommended action for the resulting
// Critical verdict.
//
// These are observation failures, not observed faults — the CEL rules
// never ran — so the action is never the check's hardware-remediation
// default. A collection timeout maps to retry_collection; any other
// collection failure (a driver, permission, or decode error) maps to
// check_collection: investigate the collection rather than remediate
// hardware. A non-timeout failure outranks a timeout when both are
// present, since a concrete collection error is more actionable than a
// slow read.
func matchingError(errs []health.ErroredMeasurement, required []string) (detail string, action health.RecommendedAction) {
	if len(errs) == 0 {
		return "", health.NoAction
	}
	var details []string
	var hasTimeout, hasOther bool
	for _, e := range errs {
		for _, sig := range required {
			if slices.Contains(e.FailedSignals(), sig) {
				details = append(details, e.Err().Error())
				if errors.Is(e.Err(), health.ErrCollectionTimeout) {
					hasTimeout = true
				} else {
					hasOther = true
				}
				break
			}
		}
	}
	switch {
	case hasOther:
		action = health.Drain(health.DiagnosticActionCheckCollection)
	case hasTimeout:
		action = health.Drain(health.DiagnosticActionRetryCollection)
	}
	return strings.Join(details, "; "), action
}

func skippedVerdict(cd *health.CheckDefinition, ts time.Time, reason health.SkipReason, detail string) health.CheckVerdict {
	return health.CheckVerdict{
		CheckID:        cd.ID,
		Skipped:        true,
		SkipReason:     reason,
		SkipDetail:     detail,
		Category:       cd.Category,
		MaturityLevel:  cd.MaturityLevel,
		NVIDIAEndorsed: cd.Provenance == health.NVIDIA,
		Timestamp:      ts,
	}
}

// ErroredVerdict builds a Critical CheckVerdict for a check whose input
// measurement could not be observed — the engine emits one when a sensor's
// errored measurement satisfies the check's schema (architecture, context,
// RequiredSignals) but the data is unavailable. action is the
// collection-failure remediation; it is never the check's fault default,
// which applies only when a rule actually matched.
func ErroredVerdict(cd *health.CheckDefinition, ts time.Time, detail string, action health.RecommendedAction) health.CheckVerdict {
	return health.CheckVerdict{
		CheckID:           cd.ID,
		Severity:          health.Critical,
		Messages:          []string{fmt.Sprintf("measurement unavailable: %s", detail)},
		Category:          cd.Category,
		MaturityLevel:     cd.MaturityLevel,
		NVIDIAEndorsed:    cd.Provenance == health.NVIDIA,
		RecommendedAction: action,
		Timestamp:         ts,
	}
}

func interpolateMessage(template string, vars map[string]any) string {
	result := template
	for name, val := range vars {
		result = strings.ReplaceAll(result, "${"+name+"}", fmt.Sprint(val))
	}
	return result
}

// TopologicalOrder returns check IDs ordered so that dependencies come
// before the checks that depend on them. IDs are sorted before traversal
// for deterministic output.
func TopologicalOrder(checks map[string]*health.CheckDefinition) []string {
	visited := make(map[string]bool, len(checks))
	var order []string

	ids := make([]string, 0, len(checks))
	for id := range checks {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		if cd, ok := checks[id]; ok {
			for _, dep := range cd.Dependencies {
				visit(dep)
			}
		}
		order = append(order, id)
	}

	for _, id := range ids {
		visit(id)
	}
	return order
}
