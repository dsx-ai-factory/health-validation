// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"fmt"
)

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

	// PCIe groups checks related to PCI Express.
	PCIe

	// InfiniBand covers InfiniBand network health: port state,
	// error rates, and link integrity.
	InfiniBand

	// Storage covers storage health in NVIDIA-validated
	// configurations: NVMe state, SMART counters.
	Storage

	// Node covers host-wide GPU-system signals that are not bound to any
	// single device — GPU count, driver version, and similar inventory checks.
	Node

	// Xid covers NVRM Xid fault events logged by the NVIDIA driver to
	// the kernel ring buffer. Xid measurements are collected separately
	// from GPU telemetry and route only to Xid checks.
	Xid

	// CPU covers host processor inventory and availability signals.
	CPU
)

var categoryNames = map[Category]string{
	GPU: "gpu", NVLink: "nvlink", PCIe: "pcie", InfiniBand: "infiniband", Storage: "storage", Node: "node", Xid: "xid", CPU: "cpu",
}

var categoryFromName = map[string]Category{
	"gpu": GPU, "nvlink": NVLink, "pcie": PCIe, "infiniband": InfiniBand, "storage": Storage, "node": Node, "xid": Xid, "cpu": CPU,
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

// MarshalText encodes category names when Category is used as a map key.
func (c Category) MarshalText() ([]byte, error) {
	name, ok := categoryNames[c]
	if !ok {
		return nil, fmt.Errorf("unknown category value %d", int(c))
	}
	return []byte(name), nil
}

// UnmarshalText decodes category names when Category is used as a map key.
func (c *Category) UnmarshalText(text []byte) error {
	if category, ok := categoryFromName[string(text)]; ok {
		*c = category
		return nil
	}

	return fmt.Errorf("unknown category %q", text)
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

// SchedulingCadence describes how often a check is meant to run in a node's
// operational lifecycle. The cadences nest by frequency —
// Continuous > Prolog > MaintenanceWindow — so a check declared at a more
// frequent cadence is also eligible to run in every less frequent window: a
// Continuous check runs at prolog and in maintenance windows, a Prolog check
// runs in maintenance windows. See [SchedulingCadence.Covers].
//
// Continuous is the zero value: a check that omits SchedulingCadence defaults
// to the most frequent cadence, so it runs in every window rather than being
// silently excluded from more frequent ones.
type SchedulingCadence int

const (
	Continuous        SchedulingCadence = iota // During normal operation (most frequent); the zero-value default.
	Prolog                                     // Before job dispatch.
	MaintenanceWindow                          // During scheduled maintenance (least frequent).
)

var schedulingCadenceNames = map[SchedulingCadence]string{
	Prolog: "PROLOG", Continuous: "CONTINUOUS", MaintenanceWindow: "MAINTENANCE",
}

var schedulingCadenceFromName = map[string]SchedulingCadence{
	"PROLOG": Prolog, "CONTINUOUS": Continuous, "MAINTENANCE": MaintenanceWindow,
}

// cadenceFrequency ranks cadences by how often they run; a higher rank means
// more frequent. Continuous > Prolog > MaintenanceWindow. The int enum values
// are not this order (they are wire-stable), so the ranking is explicit; an
// unknown cadence ranks lowest (0).
var cadenceFrequency = map[SchedulingCadence]int{
	Continuous:        2,
	Prolog:            1,
	MaintenanceWindow: 0,
}

// Covers reports whether a check scheduled at sc should run when checks for the
// requested cadence are collected. Because cadences nest by frequency
// (Continuous > Prolog > MaintenanceWindow), a check runs at any cadence no
// more frequent than its own: filtering to Prolog returns Continuous and
// Prolog checks (not MaintenanceWindow), and filtering to MaintenanceWindow
// returns every check.
func (sc SchedulingCadence) Covers(requested SchedulingCadence) bool {
	return cadenceFrequency[sc] >= cadenceFrequency[requested]
}

// SchedulingCadenceFromName maps a wire name (CONTINUOUS, PROLOG,
// MAINTENANCE) to the enum. The match is exact; unknown names error.
func SchedulingCadenceFromName(name string) (SchedulingCadence, error) {
	v, ok := schedulingCadenceFromName[name]
	if !ok {
		return 0, fmt.Errorf("unknown scheduling cadence %q", name)
	}
	return v, nil
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

// ExecutionContextFromName maps a wire name (OPERATOR_INFRA, TENANT_GUEST,
// OUT_OF_BAND) to the enum. The match is exact; unknown names error.
func ExecutionContextFromName(name string) (ExecutionContext, error) {
	v, ok := executionContextFromName[name]
	if !ok {
		return 0, fmt.Errorf("unknown execution context %q", name)
	}
	return v, nil
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
	InsufficientHistory                       // Time-window data not yet available.
	ThresholdUnconfigured                     // Required threshold not supplied for this fleet.
	NoApplicableMeasurement                   // No supplied measurement carries the check's required signals.

	// SensorMissing marks a check whose whole category was expected but
	// never arrived: the sensor is absent, unconfigured, or its backend
	// was never constructed. It is distinct from
	// NoApplicableMeasurement, which means the category was collected
	// but no measurement carried this check's required signals, and from
	// a Critical errored verdict, which means the sensor ran and failed.
	SensorMissing
	CadenceMismatch // Check is not scheduled for the requested cadence.
)

var skipReasonNames = map[SkipReason]string{
	NotSkipped: "NOT_SKIPPED", ArchitectureUnsupported: "SIGNAL_UNAVAILABLE_ARCHITECTURE",
	DeploymentUnsupported: "SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL", CheckDisabled: "CHECK_DISABLED",
	InsufficientHistory:   "INSUFFICIENT_HISTORY",
	ThresholdUnconfigured: "THRESHOLD_UNCONFIGURED", NoApplicableMeasurement: "NO_APPLICABLE_MEASUREMENT",
	CadenceMismatch: "CADENCE_MISMATCH", SensorMissing: "SENSOR_MISSING",
}

var skipReasonFromName = map[string]SkipReason{
	"NOT_SKIPPED": NotSkipped, "SIGNAL_UNAVAILABLE_ARCHITECTURE": ArchitectureUnsupported,
	"SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL": DeploymentUnsupported, "CHECK_DISABLED": CheckDisabled,
	"INSUFFICIENT_HISTORY":   InsufficientHistory,
	"THRESHOLD_UNCONFIGURED": ThresholdUnconfigured, "NO_APPLICABLE_MEASUREMENT": NoApplicableMeasurement,
	"CADENCE_MISMATCH": CadenceMismatch, "SENSOR_MISSING": SensorMissing,
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

// --- MaturityLevel ---

// MaturityLevel describes how production-ready a check is.
type MaturityLevel int

const (
	Stable       MaturityLevel = iota // Vetted for production use.
	Experimental                      // Under evaluation; may change or be withdrawn.
)

var maturityLevelNames = map[MaturityLevel]string{
	Stable: "stable", Experimental: "experimental",
}

var maturityLevelFromName = map[string]MaturityLevel{
	"stable": Stable, "experimental": Experimental,
}

func (ml MaturityLevel) String() string {
	if name, ok := maturityLevelNames[ml]; ok {
		return name
	}
	return fmt.Sprintf("MaturityLevel(%d)", int(ml))
}

func (ml MaturityLevel) MarshalJSON() ([]byte, error) { return marshalEnum(ml, maturityLevelNames) }
func (ml *MaturityLevel) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, maturityLevelFromName, ml, "maturity level")
}

// --- ConditionScope ---

// ConditionScope records how far the condition a check detects reaches: the
// smallest boundary that must be inspected to explain it. It is independent of
// [RecommendedAction], which stays node-local: a ClusterScope verdict reports
// what the fault implicates, not that the cluster should be acted on.
//
// NodeScope is the zero value, so a check that omits it is treated as
// node-local, which is how consumers already treat every check.
//
// The NODE value is distinct from the [Node] Category: one is a blast radius,
// the other a hardware domain. They serialize with different casing.
type ConditionScope int

const (
	NodeScope    ConditionScope = iota // This node's own hardware explains the condition.
	DomainScope                        // Explaining it requires the NVLink domain shared with peer nodes.
	ClusterScope                       // Explaining it requires the cluster network fabric beyond the node.
)

var conditionScopeNames = map[ConditionScope]string{
	NodeScope: "NODE", DomainScope: "DOMAIN", ClusterScope: "CLUSTER",
}

var conditionScopeFromName = map[string]ConditionScope{
	"NODE": NodeScope, "DOMAIN": DomainScope, "CLUSTER": ClusterScope,
}

func (cs ConditionScope) String() string {
	if name, ok := conditionScopeNames[cs]; ok {
		return name
	}
	return fmt.Sprintf("ConditionScope(%d)", int(cs))
}

func (cs ConditionScope) MarshalJSON() ([]byte, error) { return marshalEnum(cs, conditionScopeNames) }
func (cs *ConditionScope) UnmarshalJSON(data []byte) error {
	return unmarshalEnum(data, conditionScopeFromName, cs, "condition scope")
}
