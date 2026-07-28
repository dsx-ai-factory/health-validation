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

import (
	"encoding/json"
	"fmt"
)

// OperationalState names the node-level state change a verdict
// recommends. It is the high-level "what should happen to the node?"
// answer; the [DiagnosticAction] companion narrows the why when the
// state is [OperationalDrain].
type OperationalState int

const (
	// OperationalNone is the zero value: no node-level action
	// recommended.
	OperationalNone OperationalState = iota

	// OperationalMonitor keeps the node in service but flags the
	// finding for trending; no action required right now.
	OperationalMonitor

	// OperationalDrain removes the node from scheduling so the
	// finding can be remediated. The accompanying [DiagnosticAction]
	// names the specific remediation.
	OperationalDrain
)

var operationalStateNames = map[OperationalState]string{
	OperationalNone:    "none",
	OperationalMonitor: "monitor",
	OperationalDrain:   "drain",
}

var operationalStateFromName = map[string]OperationalState{
	"none":    OperationalNone,
	"monitor": OperationalMonitor,
	"drain":   OperationalDrain,
}

func (s OperationalState) String() string {
	if name, ok := operationalStateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("OperationalState(%d)", int(s))
}

func (s OperationalState) MarshalJSON() ([]byte, error) {
	return marshalEnum(s, operationalStateNames)
}

func (s *OperationalState) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, operationalStateFromName, s, "operational state")
}

// DiagnosticAction is the finer-grained remediation tag carried
// alongside [OperationalDrain]. It is meaningful only when the
// companion [OperationalState] is [OperationalDrain]; verdicts elide
// it from JSON otherwise.
type DiagnosticAction int

const (
	// DiagnosticActionUnspecified is the zero value.
	DiagnosticActionUnspecified DiagnosticAction = iota

	DiagnosticActionContactSupport
	DiagnosticActionReboot
	DiagnosticActionResetGPU
	DiagnosticActionRestartApp
	DiagnosticActionRestartBM
	DiagnosticActionRestartVM
	DiagnosticActionUpdateSWFW
	DiagnosticActionCheckMechanicals
	DiagnosticActionCheckUVM
	DiagnosticActionWorkflowXid45
	DiagnosticActionWorkflowXid48
	DiagnosticActionWorkflowNVLinkErr
	DiagnosticActionWorkflowNVLink5Err
	DiagnosticActionXid154
	DiagnosticActionRetestHardware

	// DiagnosticActionRetryCollection marks a verdict whose data could not
	// be collected in time (a sensor timeout) rather than a confirmed
	// fault: the node should retry collection or be investigated before
	// any hardware remediation.
	DiagnosticActionRetryCollection

	// DiagnosticActionCheckCollection marks a verdict whose data could not
	// be collected for a non-timeout reason (a driver, permission, or
	// decode failure). The collection mechanism itself failed, so the fix
	// is to investigate it — not to remediate hardware that was never
	// observed to be faulty.
	DiagnosticActionCheckCollection
)

var diagnosticActionNames = map[DiagnosticAction]string{
	DiagnosticActionContactSupport:     "contact_support",
	DiagnosticActionReboot:             "reboot",
	DiagnosticActionResetGPU:           "reset_gpu",
	DiagnosticActionRestartApp:         "restart_app",
	DiagnosticActionRestartBM:          "restart_bm",
	DiagnosticActionRestartVM:          "restart_vm",
	DiagnosticActionUpdateSWFW:         "update_swfw",
	DiagnosticActionCheckMechanicals:   "check_mechanicals",
	DiagnosticActionCheckUVM:           "check_uvm",
	DiagnosticActionWorkflowXid45:      "workflow_xid_45",
	DiagnosticActionWorkflowXid48:      "workflow_xid_48",
	DiagnosticActionWorkflowNVLinkErr:  "workflow_nvlink_err",
	DiagnosticActionWorkflowNVLink5Err: "workflow_nvlink5_err",
	DiagnosticActionXid154:             "xid_154",
	DiagnosticActionRetestHardware:     "retest_hardware",
	DiagnosticActionRetryCollection:    "retry_collection",
	DiagnosticActionCheckCollection:    "check_collection",
}

var diagnosticActionFromName = map[string]DiagnosticAction{
	"contact_support":      DiagnosticActionContactSupport,
	"reboot":               DiagnosticActionReboot,
	"reset_gpu":            DiagnosticActionResetGPU,
	"restart_app":          DiagnosticActionRestartApp,
	"restart_bm":           DiagnosticActionRestartBM,
	"restart_vm":           DiagnosticActionRestartVM,
	"update_swfw":          DiagnosticActionUpdateSWFW,
	"check_mechanicals":    DiagnosticActionCheckMechanicals,
	"check_uvm":            DiagnosticActionCheckUVM,
	"workflow_xid_45":      DiagnosticActionWorkflowXid45,
	"workflow_xid_48":      DiagnosticActionWorkflowXid48,
	"workflow_nvlink_err":  DiagnosticActionWorkflowNVLinkErr,
	"workflow_nvlink5_err": DiagnosticActionWorkflowNVLink5Err,
	"xid_154":              DiagnosticActionXid154,
	"retest_hardware":      DiagnosticActionRetestHardware,
	"retry_collection":     DiagnosticActionRetryCollection,
	"check_collection":     DiagnosticActionCheckCollection,
}

func (a DiagnosticAction) String() string {
	if name, ok := diagnosticActionNames[a]; ok {
		return name
	}
	if a == DiagnosticActionUnspecified {
		return ""
	}
	return fmt.Sprintf("DiagnosticAction(%d)", int(a))
}

// diagnosticActionPriority ranks diagnostic actions by remediation
// authority for node-level aggregation: when several checks tie at the
// top severity and all recommend [OperationalDrain], the action earliest
// in this list is the one that surfaces on the node verdict. Lower index
// = higher priority.
var diagnosticActionPriority = []DiagnosticAction{
	DiagnosticActionContactSupport,
	DiagnosticActionCheckMechanicals,
	DiagnosticActionRetestHardware,
	DiagnosticActionWorkflowXid45,
	DiagnosticActionWorkflowXid48,
	DiagnosticActionWorkflowNVLinkErr,
	DiagnosticActionWorkflowNVLink5Err,
	DiagnosticActionCheckUVM,
	DiagnosticActionUpdateSWFW,
	DiagnosticActionResetGPU,
	DiagnosticActionRestartVM,
	DiagnosticActionRestartBM,
	DiagnosticActionRestartApp,
	DiagnosticActionReboot,
	DiagnosticActionXid154,
	// Collection failures rank last: an observed fault outranks "could not
	// observe", and a concrete failure (check_collection) outranks a timeout
	// (retry_collection).
	DiagnosticActionCheckCollection,
	DiagnosticActionRetryCollection,
}

// priorityRank returns this action's index in diagnosticActionPriority,
// or len(list) for actions not present (ranking them last). Lower is
// higher priority.
func (a DiagnosticAction) priorityRank() int {
	for i, d := range diagnosticActionPriority {
		if d == a {
			return i
		}
	}
	return len(diagnosticActionPriority)
}

func (a DiagnosticAction) MarshalJSON() ([]byte, error) {
	return marshalEnum(a, diagnosticActionNames)
}

func (a *DiagnosticAction) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, diagnosticActionFromName, a, "diagnostic action")
}

// RecommendedAction is the structured remediation a check carries.
// OperationalState is the high-level state change to apply; DiagnosticAction
// names the specific remediation and is only valid (and only emitted in
// JSON) when OperationalState is [OperationalDrain].
//
// Use [Drain] / [Monitor] / [NoAction] for readable call sites instead
// of constructing the struct directly.
type RecommendedAction struct {
	OperationalState OperationalState `json:"operational_state"`
	DiagnosticAction DiagnosticAction `json:"diagnostic_action,omitzero"`
}

// IsZero reports whether this RecommendedAction carries no
// recommendation. Container types use it via the `omitzero` JSON tag
// to elide the field entirely when no action applies. A non-zero
// OperationalState (anything other than [OperationalNone]) makes the
// recommendation non-zero regardless of DiagnosticAction.
func (r RecommendedAction) IsZero() bool {
	return r.OperationalState == OperationalNone
}

// MarshalJSON enforces the invariant that DiagnosticAction is emitted
// only when OperationalState is [OperationalDrain]. A non-Drain state
// always serializes without a diagnostic_action key, even if the
// in-memory value carries one.
func (r RecommendedAction) MarshalJSON() ([]byte, error) {
	if r.OperationalState != OperationalDrain {
		r.DiagnosticAction = DiagnosticActionUnspecified
	}
	type alias RecommendedAction
	return json.Marshal(alias(r))
}

// outranks reports whether r should win over other when selecting a
// single node-level action from checks that tie at the top severity. The
// higher [OperationalState] wins first (drain > monitor > none);
// when both are [OperationalDrain] the higher-priority [DiagnosticAction]
// wins. Equal candidates return false, so the first one encountered is kept.
func (r RecommendedAction) outranks(other RecommendedAction) bool {
	if r.OperationalState != other.OperationalState {
		return r.OperationalState > other.OperationalState
	}
	if r.OperationalState == OperationalDrain {
		return r.DiagnosticAction.priorityRank() < other.DiagnosticAction.priorityRank()
	}
	return false
}

// Drain returns a RecommendedAction with [OperationalDrain] and the
// given DiagnosticAction.
func Drain(d DiagnosticAction) RecommendedAction {
	return RecommendedAction{OperationalState: OperationalDrain, DiagnosticAction: d}
}

// Monitor is the canonical "keep in service, watch the trend"
// recommendation.
var Monitor = RecommendedAction{OperationalState: OperationalMonitor}

// NoAction is the zero RecommendedAction.
var NoAction = RecommendedAction{}
