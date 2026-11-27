// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package catalogview

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// MaxRecommenders caps the checks listed for one action.
const MaxRecommenders = 30

// MaxDescribeBytes caps one hv_describe_check result.
const MaxDescribeBytes = 16 << 10

// OperationalStates are the states a remediation lookup can filter on.
func OperationalStates() []string { return []string{"ready", "monitor", "drain"} }

// actionMeanings says what health-validation means by each step. The keys must match
// RemediationActions, which is generated from catalog/health; a test checks both.
var actionMeanings = map[string]string{
	"service_required": "No automated step is left. The node needs hardware or software investigation " +
		"through the operator's own service path. It is the last step of a sequence.",
	"reset_gpu": "Reset the affected GPU, the lightest automated step. Engines that cannot reset one GPU " +
		"skip to the next step.",
	"restart_app": "Restart the application or job that hit the fault.",
	"restart_os":  "Restart the node's operating system.",
	"update_swfw": "Update the GPU driver, firmware or other software named in the guidance.",
	"stop_p2p_traffic": "Quiesce peer-to-peer traffic on the GPU: no new jobs, stop the processes doing P2P " +
		"traffic, and turn off UVM persistence mode.",
	"hold": "Keep the node drained but do not remediate it alone. When enough nodes in the same NVLink or " +
		"rack domain are held (3 is the recommended threshold), remediate the domain together. Used for " +
		"NVLink fabric faults that are more often a shared switch than one node.",
	"retry_collection": "The data could not be collected in time, which is not a confirmed fault. Collect " +
		"again, or investigate, before any hardware step.",
	"check_collection": "Collection failed for a reason other than a timeout, such as a driver, permission " +
		"or decode failure. Fix the collection path; the hardware was never seen to be faulty.",
}

// Remediation explains one remediation step.
type Remediation struct {
	Action           string   `json:"action"`
	Meaning          string   `json:"meaning"`
	OperationalState string   `json:"operational_state_filter"`
	CatalogVersion   string   `json:"catalog_version"`
	Note             string   `json:"note"`
	AssignedBy       string   `json:"assigned_by,omitempty" jsonschema:"set when the engine, not a check definition, gives this step"`
	RecommendedBy    []string `json:"recommended_by" jsonschema:"check IDs in the compiled-in catalog that recommend this step"`
	Total            int      `json:"total" jsonschema:"how many checks recommend it, before the cap"`
	Truncated        bool     `json:"truncated"`
}

// engineActions are steps no check definition names: the engine gives them to any
// check it could not collect for, so recommended_by is empty for them.
var engineActions = map[string]string{
	"retry_collection": "health-validation's engine, for any check whose signals timed out during " +
		"collection; serve's collection_source_busy verdict uses it too. No check definition names it, " +
		"so recommended_by is empty.",
	"check_collection": "health-validation's engine, for any check whose signals failed to collect for a " +
		"reason other than a timeout. No check definition names it, so recommended_by is empty.",
}

const remediationNote = "Neither health-validation nor this server performs any step. They only " +
	"recommend; the scheduler or a remediation engine acts."

// Explain says what an action means and which checks recommend it. state filters on the
// operational state the recommendation comes with, or is Any.
func Explain(action, state string) (*Remediation, error) {
	meaning, ok := actionMeanings[action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", action)
	}

	if state == "" {
		state = Any
	}

	if state != Any && !slices.Contains(OperationalStates(), state) {
		return nil, fmt.Errorf("unknown operational state %q", state)
	}

	var ids []string

	for _, definition := range Catalog().List(catalog.Filter{}) {
		if recommends(definition, health.RemediationAction(action), state) {
			ids = append(ids, definition.ID)
		}
	}

	out := &Remediation{
		Action: action, Meaning: meaning, OperationalState: state, CatalogVersion: Version(),
		Note: remediationNote, RecommendedBy: []string{}, Total: len(ids), AssignedBy: engineActions[action],
	}

	if len(ids) > MaxRecommenders {
		ids, out.Truncated = ids[:MaxRecommenders], true
	}

	out.RecommendedBy = append(out.RecommendedBy, ids...)

	return out, nil
}

func recommends(definition *health.CheckDefinition, action health.RemediationAction, state string) bool {
	matches := func(recommended health.RecommendedAction) bool {
		return slices.Contains(recommended.RemediationActions, action) &&
			(state == Any || recommended.OperationalState.String() == state)
	}

	if matches(definition.RecommendedAction) {
		return true
	}

	for _, rule := range definition.Rules {
		if rule.HasRecommendedAction() && matches(rule.RecommendedAction) {
			return true
		}
	}

	return false
}

// fit cuts a description to MaxDescribeBytes, dropping per-architecture values first,
// then thresholds and rules from the end, and marks it truncated.
func fit(description *Description) {
	size := func() int {
		raw, _ := json.Marshal(description)
		return len(raw)
	}

	if size() <= MaxDescribeBytes {
		return
	}

	description.Truncated = true

	for i := range description.Thresholds {
		description.Thresholds[i].PerArchitecture = nil
	}

	for size() > MaxDescribeBytes && len(description.Thresholds) > 0 {
		description.Thresholds = description.Thresholds[:len(description.Thresholds)-1]
	}

	for size() > MaxDescribeBytes && len(description.Rules) > 1 {
		description.Rules = description.Rules[:len(description.Rules)-1]
	}
}
