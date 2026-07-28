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

import "fmt"

// --- Severity ---

// Severity represents the graduated health severity of a check result.
// The levels form a total order: Pass < Info < Warning < Critical.
// Unknown is a special case indicating the check could not complete.
type Severity int

const (
	// Pass indicates no condition detected. The monitored state
	// is within normal operating bounds.
	Pass Severity = iota

	// Info indicates an anomaly detected within safe operating bounds.
	// The node may remain in service; monitor for trend.
	Info

	// Warning indicates a condition that degrades performance or
	// increases failure risk. The node may continue in service for
	// a bounded window pending operator action.
	Warning

	// Critical indicates imminent risk of failure, data corruption,
	// or unrecoverable state. The node should be removed from service
	// without delay.
	Critical

	// Unknown indicates the check could not complete. Service
	// eligibility for the affected component cannot be assessed.
	// This typically results from tool failures or insufficient data.
	Unknown
)

var severityNames = map[Severity]string{
	Pass: "PASS", Info: "INFO", Warning: "WARNING", Critical: "CRITICAL", Unknown: "UNKNOWN",
}

var severityFromName = map[string]Severity{
	"PASS": Pass, "INFO": Info, "WARNING": Warning, "CRITICAL": Critical, "UNKNOWN": Unknown,
}

func (s Severity) String() string {
	if name, ok := severityNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

func (s Severity) MarshalJSON() ([]byte, error) { return marshalEnum(s, severityNames) }
func (s *Severity) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, severityFromName, s, "severity")
}

// Worse returns the more severe of two severity levels.
func Worse(a, b Severity) Severity {
	if a > b {
		return a
	}
	return b
}

// --- Category ---

// Category groups related checks by the hardware or interconnect
// domain they monitor. It determines how failure domains are
// reported in node-level verdicts.
type Category int

const (
	// GPU covers GPU device health: driver-reported events (XID),
	// thermal state, driver status, power, and clock behavior.
	GPU Category = iota

	// NVLink covers NVLink interconnect health: replay counters,
	// link errors, and fabric state.
	NVLink

	// PCIe covers PCI Express bus health: link width, speed
	// degradation, and error counters.
	PCIe

	// InfiniBand covers InfiniBand network health: port state,
	// error rates, and link integrity.
	InfiniBand

	// Storage covers storage health in NVIDIA-validated
	// configurations: NVMe state, SMART counters.
	Storage

	// Node covers host-wide signals that are not bound to any single
	// device — GPU count, driver version, and similar inventory checks.
	Node

	// Xid covers NVRM Xid fault events logged by the NVIDIA driver to
	// the kernel ring buffer. Xid measurements are collected separately
	// from GPU telemetry and route only to Xid checks.
	Xid
)

var categoryNames = map[Category]string{
	GPU: "gpu", NVLink: "nvlink", PCIe: "pcie", InfiniBand: "infiniband", Storage: "storage", Node: "node", Xid: "xid",
}

var categoryFromName = map[string]Category{
	"gpu": GPU, "nvlink": NVLink, "pcie": PCIe, "infiniband": InfiniBand, "storage": Storage, "node": Node, "xid": Xid,
}

func (c Category) String() string {
	if name, ok := categoryNames[c]; ok {
		return name
	}
	return fmt.Sprintf("Category(%d)", int(c))
}

func (c Category) MarshalJSON() ([]byte, error) { return marshalEnum(c, categoryNames) }
func (c *Category) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, categoryFromName, c, "category")
}

// --- Provenance ---

// Provenance identifies who authored a check definition and determines
// the trust level of its verdicts.
type Provenance int

const (
	NVIDIA    Provenance = iota // Authored and maintained by NVIDIA.
	Community                   // Community-contributed, NVIDIA-reviewed.
	Private                     // Privately authored for a specific deployment.
)

var provenanceNames = map[Provenance]string{
	NVIDIA: "nvidia", Community: "community", Private: "private",
}

var provenanceFromName = map[string]Provenance{
	"nvidia": NVIDIA, "community": Community, "private": Private,
}

func (p Provenance) String() string {
	if name, ok := provenanceNames[p]; ok {
		return name
	}
	return fmt.Sprintf("Provenance(%d)", int(p))
}

func (p Provenance) MarshalJSON() ([]byte, error) { return marshalEnum(p, provenanceNames) }
func (p *Provenance) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, provenanceFromName, p, "provenance")
}

// --- SchedulingCadence ---

// SchedulingCadence describes when a check should be run in the
// operational lifecycle of a node.
type SchedulingCadence int

const (
	Prolog            SchedulingCadence = iota // Before job dispatch.
	Continuous                                 // During normal operation.
	TenantTurnover                             // Between tenant transitions.
	MaintenanceWindow                          // During scheduled maintenance.
)

var schedulingCadenceNames = map[SchedulingCadence]string{
	Prolog: "PROLOG", Continuous: "CONTINUOUS", TenantTurnover: "TENANT_TURNOVER", MaintenanceWindow: "MAINTENANCE",
}

var schedulingCadenceFromName = map[string]SchedulingCadence{
	"PROLOG": Prolog, "CONTINUOUS": Continuous, "TENANT_TURNOVER": TenantTurnover, "MAINTENANCE": MaintenanceWindow,
}

func (sc SchedulingCadence) String() string {
	if name, ok := schedulingCadenceNames[sc]; ok {
		return name
	}
	return fmt.Sprintf("SchedulingCadence(%d)", int(sc))
}

func (sc SchedulingCadence) MarshalJSON() ([]byte, error) {
	return marshalEnum(sc, schedulingCadenceNames)
}

func (sc *SchedulingCadence) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, schedulingCadenceFromName, sc, "scheduling cadence")
}

// --- ExecutionContext ---

// ExecutionContext identifies where a health check can run within a
// GPU cluster.
type ExecutionContext int

const (
	OperatorInfra ExecutionContext = iota // Host OS or CSP privileged control-plane VM.
	TenantGuest                           // Tenant VM, container, or K8s worker node.
	OutOfBand                             // BMC via Redfish API.
)

var executionContextNames = map[ExecutionContext]string{
	OperatorInfra: "OPERATOR_INFRA", TenantGuest: "TENANT_GUEST", OutOfBand: "OUT_OF_BAND",
}

var executionContextFromName = map[string]ExecutionContext{
	"OPERATOR_INFRA": OperatorInfra, "TENANT_GUEST": TenantGuest, "OUT_OF_BAND": OutOfBand,
}

func (ec ExecutionContext) String() string {
	if name, ok := executionContextNames[ec]; ok {
		return name
	}
	return fmt.Sprintf("ExecutionContext(%d)", int(ec))
}

func (ec ExecutionContext) MarshalJSON() ([]byte, error) {
	return marshalEnum(ec, executionContextNames)
}

func (ec *ExecutionContext) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, executionContextFromName, ec, "execution context")
}

// --- ExecutionClass ---

// ExecutionClass distinguishes passive observation from active probing.
type ExecutionClass int

const (
	Passive ExecutionClass = iota // Reads existing state without perturbing the system.
	Active                        // May exercise hardware or run diagnostic commands.
)

var executionClassNames = map[ExecutionClass]string{
	Passive: "PASSIVE", Active: "ACTIVE",
}

var executionClassFromName = map[string]ExecutionClass{
	"PASSIVE": Passive, "ACTIVE": Active,
}

func (ec ExecutionClass) String() string {
	if name, ok := executionClassNames[ec]; ok {
		return name
	}
	return fmt.Sprintf("ExecutionClass(%d)", int(ec))
}

func (ec ExecutionClass) MarshalJSON() ([]byte, error) { return marshalEnum(ec, executionClassNames) }
func (ec *ExecutionClass) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, executionClassFromName, ec, "execution class")
}

// --- SkipReason ---

// SkipReason provides a machine-readable code for why a check did not run.
type SkipReason int

const (
	NotSkipped              SkipReason = iota // Check ran normally.
	ArchitectureUnsupported                   // Wrong GPU architecture.
	DeploymentUnsupported                     // Requires different execution context.
	CheckDisabled                             // Operator disabled this check.
	DependencySkipped                         // Prerequisite check was skipped.
	InsufficientHistory                       // Time-window data not yet available.
)

var skipReasonNames = map[SkipReason]string{
	NotSkipped: "NOT_SKIPPED", ArchitectureUnsupported: "SIGNAL_UNAVAILABLE_ARCHITECTURE",
	DeploymentUnsupported: "SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL", CheckDisabled: "CHECK_DISABLED",
	DependencySkipped: "DEPENDENCY_SKIPPED", InsufficientHistory: "INSUFFICIENT_HISTORY",
}

var skipReasonFromName = map[string]SkipReason{
	"NOT_SKIPPED": NotSkipped, "SIGNAL_UNAVAILABLE_ARCHITECTURE": ArchitectureUnsupported,
	"SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL": DeploymentUnsupported, "CHECK_DISABLED": CheckDisabled,
	"DEPENDENCY_SKIPPED": DependencySkipped, "INSUFFICIENT_HISTORY": InsufficientHistory,
}

func (sr SkipReason) String() string {
	if name, ok := skipReasonNames[sr]; ok {
		return name
	}
	return fmt.Sprintf("SkipReason(%d)", int(sr))
}

func (sr SkipReason) MarshalJSON() ([]byte, error) { return marshalEnum(sr, skipReasonNames) }
func (sr *SkipReason) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, skipReasonFromName, sr, "skip reason")
}
