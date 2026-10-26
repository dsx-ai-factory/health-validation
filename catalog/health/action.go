// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"encoding/json"
	"fmt"
	"slices"
)

// OperationalState names the node-level state change a verdict
// recommends. It is the high-level "what should happen to the node?"
// answer; the [RemediationAction] companion sequence narrows the why when
// the state is [OperationalDrain].
type OperationalState int

const (
	// OperationalReady is the zero value: the node stays ready for work
	// and no node-level action is recommended.
	OperationalReady OperationalState = iota

	// OperationalMonitor keeps the node in service but flags the
	// finding for trending; no action required right now.
	OperationalMonitor

	// OperationalDrain removes the node from scheduling so the
	// finding can be remediated. The accompanying [RemediationAction]
	// sequence names the specific remediation steps.
	OperationalDrain
)

var operationalStateNames = map[OperationalState]string{
	OperationalReady:   "ready",
	OperationalMonitor: "monitor",
	OperationalDrain:   "drain",
}

var operationalStateFromName = map[string]OperationalState{
	"ready":   OperationalReady,
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

// RemediationAction is a single step in the remediation sequence carried
// alongside [OperationalDrain]. It is meaningful only when the companion
// [OperationalState] is [OperationalDrain]; verdicts elide the sequence
// from JSON otherwise.
//
// Unlike the enum types in this package, RemediationAction is an open
// string type: the constants below name the steps NVIDIA's own catalog
// entries use, but a catalog extended with custom checks may recommend
// any string value here.
type RemediationAction string

const (
	// RemediationActionServiceRequired is the last step in a sequence: no
	// automated remediation remains, and the node needs hardware or
	// software investigation through the operator's own service path.
	RemediationActionServiceRequired RemediationAction = "service_required"

	RemediationActionResetGPU   RemediationAction = "reset_gpu"
	RemediationActionRestartApp RemediationAction = "restart_app"
	RemediationActionRestartOS  RemediationAction = "restart_os"
	RemediationActionUpdateSWFW RemediationAction = "update_swfw"
	RemediationActionXid154     RemediationAction = "xid_154"

	// RemediationActionHold marks a node as drained but not yet
	// individually remediated: hold it out of scheduling until a
	// threshold number of other nodes in the same NVLink/rack domain
	// have also been held (3 is the recommended threshold), at which
	// point the whole domain should be remediated together rather than
	// node-by-node. Used for NVLink fabric/route faults that are more
	// often a shared-switch problem than a single-node one.
	RemediationActionHold RemediationAction = "hold"

	// RemediationActionRetryCollection marks a verdict whose data could not
	// be collected in time (a sensor timeout) rather than a confirmed
	// fault: the node should retry collection or be investigated before
	// any hardware remediation.
	RemediationActionRetryCollection RemediationAction = "retry_collection"

	// RemediationActionCheckCollection marks a verdict whose data could not
	// be collected for a non-timeout reason (a driver, permission, or
	// decode failure). The collection mechanism itself failed, so the fix
	// is to investigate it — not to remediate hardware that was never
	// observed to be faulty.
	RemediationActionCheckCollection RemediationAction = "check_collection"
)

// RecommendedAction is the structured remediation a check carries.
// OperationalState is the high-level state change to apply; RemediationActions
// is the ordered sequence of remediation steps a cluster operator's
// remediation engine should work through, and is only meaningful (and only
// emitted in JSON) when OperationalState is [OperationalDrain].
//
// Use [Drain] / [Monitor] / [NoAction] for readable call sites instead
// of constructing the struct directly.
type RecommendedAction struct {
	RemediationActions []RemediationAction `json:"remediation_actions,omitempty"`
	OperationalState   OperationalState    `json:"operational_state"`
}

// Equal reports whether r and other carry the same OperationalState and
// RemediationActions sequence. RecommendedAction contains a slice, so it
// is not comparable with ==; use Equal instead.
func (r RecommendedAction) Equal(other RecommendedAction) bool {
	return r.OperationalState == other.OperationalState &&
		slices.Equal(r.RemediationActions, other.RemediationActions)
}

// IsZero reports whether this RecommendedAction carries no
// recommendation. Container types use it via the `omitzero` JSON tag
// to elide the field entirely when no action applies. A non-zero
// OperationalState (anything other than [OperationalReady]) makes the
// recommendation non-zero regardless of RemediationActions.
func (r RecommendedAction) IsZero() bool {
	return r.OperationalState == OperationalReady
}

// MarshalJSON enforces the invariant that RemediationActions is emitted
// only when OperationalState is [OperationalDrain]. A non-Drain state
// always serializes without a remediation_actions key, even if the
// in-memory value carries one.
func (r RecommendedAction) MarshalJSON() ([]byte, error) {
	if r.OperationalState != OperationalDrain {
		r.RemediationActions = nil
	}
	type alias RecommendedAction
	return json.Marshal(alias(r))
}

// Drain returns a RecommendedAction with [OperationalDrain] and the
// given sequence of remediation steps, in the order a remediation engine
// should attempt them.
func Drain(actions ...RemediationAction) RecommendedAction {
	return RecommendedAction{OperationalState: OperationalDrain, RemediationActions: actions}
}

// GenericDrain returns the default remediation sequence for a fault with
// no remediation-engine-confirmed bespoke handling: attempt a GPU-only
// reset (the lightest-touch automated step some remediation engines take
// before falling back to a host-level action; engines that don't
// implement one simply no-op through it), then a host-level restart,
// then escalate. Checks with confirmed engine-specific handling should
// use [Drain] directly instead.
func GenericDrain() RecommendedAction {
	return Drain(RemediationActionResetGPU, RemediationActionRestartOS, RemediationActionServiceRequired)
}

// Monitor is the canonical "keep in service, watch the trend"
// recommendation.
var Monitor = RecommendedAction{OperationalState: OperationalMonitor}

// NoAction is the zero RecommendedAction.
var NoAction = RecommendedAction{}
