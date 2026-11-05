// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package findings is how a measurement built from an event stream gives each
// check only the events it judges, so a verdict never lists another code's.
// The measurement's accumulator knows which codes each check covers and
// derives per-check [Evidence]; the engine only looks it up by check ID. It is
// internal so the contract is not public API until a second domain needs it.
package findings

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Signal is the signal a measurement carries its per-check evidence under: a
// map[string]Evidence keyed by check ID.
const Signal = "evidence"

// Keys of the observed values a check with evidence reports.
const (
	ErrorCodesKey = "error_codes"
	FindingsKey   = "findings"
)

// Finding is the events of one code, grouped. A code is hierarchical, so
// Codes lists every code the events answer to, such as "149" and "149.4";
// a check covering any of them reports the finding. Evidence is
// domain-defined and must encode as JSON.
type Finding struct {
	Evidence any      `json:"evidence"`
	Codes    []string `json:"codes"`
	Count    int64    `json:"count"`
}

// IsClaimed reports whether any code f carries is in claimed.
func (f Finding) IsClaimed(claimed map[string]bool) bool {
	return slices.ContainsFunc(f.Codes, func(code string) bool { return claimed[code] })
}

// Evidence is what a verdict reports for one check: the findings for the
// codes it covers, and which of those codes occurred.
type Evidence struct {
	Findings   []Finding `json:"findings"`
	ErrorCodes []string  `json:"error_codes"`
}

// Scope returns the findings carrying any of codes, in their order, and the
// codes among them that occurred, sorted.
func Scope(fs []Finding, codes []string) Evidence {
	var e Evidence
	for _, f := range fs {
		var hit bool
		for _, code := range f.Codes {
			if slices.Contains(codes, code) {
				hit = true
				if !slices.Contains(e.ErrorCodes, code) {
					e.ErrorCodes = append(e.ErrorCodes, code)
				}
			}
		}
		if hit {
			e.Findings = append(e.Findings, f)
		}
	}
	slices.Sort(e.ErrorCodes)
	return e
}

// Observed is the observed values of a check the measurement scoped: its own
// findings and the codes that fired, and nothing else. ok is false for a check
// the measurement did not scope, which reports what it requires as usual. The
// map is never nil, and is empty when nothing matched.
func Observed(cd *health.CheckDefinition, signals map[string]any) (observed map[string]any, ok bool) {
	byCheck, isEvidence := signals[Signal].(map[string]Evidence)
	if !isEvidence {
		return nil, false
	}
	e, scoped := byCheck[cd.ID]
	if !scoped {
		return nil, false
	}
	observed = map[string]any{}
	if len(e.ErrorCodes) > 0 {
		observed[ErrorCodesKey] = e.ErrorCodes
		observed[FindingsKey] = e.Findings
	}
	return observed, true
}
