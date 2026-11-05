// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package xid defines the measurement types for NVRM Xid errors parsed
// from the system journal. Xid errors are GPU faults the NVIDIA driver
// logs to the kernel ring buffer / syslog. A sensor reports the events it
// read as an [XidMeasurement]; the catalog's [Accumulator] derives what the
// checks read from them as an [XidDerivedMeasurement].
package xid

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/internal/findings"
)

// Signals an [XidMeasurement] carries: what a sensor observed.
const (
	SignalEvents = "events"
	SignalSince  = "since"
)

// Signals an [XidDerivedMeasurement] carries. These are the keys used in CEL
// rule expressions and must match the RequiredSignals declared in the checks.
const (
	SignalXidCounts              = "xid_counts"
	SignalFatalXidCounts         = "fatal_xid_counts"
	SignalSubXidCodeCounts       = "sub_xid_code_counts"
	SignalUnknownXidCounts       = "unknown_xid_counts"
	SignalInformationalXidCounts = "informational_xid_counts"
	SignalEvidence               = findings.Signal
	SignalUnknownFindings        = "unknown_findings"
	SignalInformationalFindings  = "informational_findings"
)

var measurementSignals = []string{SignalEvents, SignalSince}

var derivedMeasurementSignals = []string{
	SignalXidCounts,
	SignalFatalXidCounts,
	SignalSubXidCodeCounts,
	SignalUnknownXidCounts,
	SignalInformationalXidCounts,
	SignalEvidence,
	SignalUnknownFindings,
	SignalInformationalFindings,
}

// MeasurementSignals lists every signal an XidMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot query
// the journal at all, telling the catalog that no signal is
// trustworthy for this measurement.
// The returned slice is a copy.
func MeasurementSignals() []string { return slices.Clone(measurementSignals) }

// DerivedMeasurementSignals lists every signal an XidDerivedMeasurement
// carries. The returned slice is a copy.
func DerivedMeasurementSignals() []string { return slices.Clone(derivedMeasurementSignals) }

// XidEvent is a single parsed "NVRM: Xid …" log line. Message is the
// line from its "NVRM: Xid" marker onward, as the driver logged it. Pid
// and ProcessName are populated only when the driver attributed the
// fault to a process; some Xid lines carry neither. NVLink5 is populated
// only for the NVLink5/NVSwitch fault family (Xid 144-150), which
// carries a sub-component and severity instead of pid/name. GPC, TPC,
// and SM are the graphics unit an Xid 13 exception names, all nil when
// the line does not name one.
type XidEvent struct {
	NVLink5        *NVLink5Detail  `json:"nvlink5,omitempty"`
	RecoveryAction *RecoveryAction `json:"recovery_action,omitempty"`
	GPC            *int64          `json:"gpc,omitempty"`
	TPC            *int64          `json:"tpc,omitempty"`
	SM             *int64          `json:"sm,omitempty"`
	ProcessName    string          `json:"process_name"`
	PCIAddress     string          `json:"pci_address"`
	Message        string          `json:"message"`
	XidCode        int64           `json:"xid_code"`
	Pid            int64           `json:"pid"`
}

// EventGroup is the events of one identical driver line, as a verdict reports
// them: the event and how many times the line was logged.
type EventGroup struct {
	XidEvent
	Count int64 `json:"count"`
}

// RecoveryAction is the transition an Xid 154 line reports: the driver
// announcing that the recovery action it wants for a GPU has changed.
//
// Like [NVLink5Detail], every field is taken verbatim from the log
// line rather than interpreted. NVRM prints both a raw code and the
// name it expands to, e.g.
//
//	NVRM: Xid (PCI:0019:01:00): 154, GPU recovery action changed from 0x0 (None) to 0x4 (Drain and Reset)
//
// An Xid 154 does not describe a fault of its own; it reports the
// recovery action the driver now wants for the GPU after some *other*
// Xid. The codes are the driver's recovery-action enum (0x0 None, 0x1
// GPU reset, 0x4 Drain and Reset, and so on), and the name is the
// driver's display text for it, which differs between driver releases
// for the same code. Both are kept so a code this catalog does not map
// still surfaces something readable.
type RecoveryAction struct {
	FromCode string `json:"from_code"`
	FromName string `json:"from_name"`
	ToCode   string `json:"to_code"`
	ToName   string `json:"to_name"`
}

// NVLink5Detail carries the extended fields NVRM logs for the
// NVLink5/NVSwitch fault family (Xid 144-150, Blackwell-only) instead
// of the pid/name pair other Xid codes use.
//
// Every field here is directly available from the log line: IntrInfo,
// ErrorStatus, and DebugData are the raw hex strings NVRM prints; Fatal
// and SubComponent are literal text in the log line (not
// register-decoded). Anything read out of the registers, such as
// [XidEvent.SubXidCode], is derived in this package. Deciding what a given
// (code, SubComponent, ErrorStatus/IntrInfo) combination means —
// NVIDIA's documented resolution/investigation guidance — requires
// NVIDIA's Xid 144-150 decode table and the host's driver era, which is
// catalog knowledge and arrives with the code that consumes it.
type NVLink5Detail struct {
	SubComponent string   `json:"sub_component"`
	IntrInfo     string   `json:"intr_info,omitempty"`
	ErrorStatus  string   `json:"error_status,omitempty"`
	DebugData    []string `json:"debug_data,omitempty"`
	Link         int64    `json:"link"`
	Fatal        bool     `json:"fatal"`
}

// XidMeasurement is every Xid event a sensor read in the window described
// by Since, one per log line and nothing more: counting, classifying and
// grouping them is the catalog's job (see [Accumulator]), so every source of
// events is interpreted the same way. One measurement is produced per sensor
// pass, even when no events were found, so a check always runs and can report
// PASS. No check reads it directly; they read the [XidDerivedMeasurement].
type XidMeasurement struct {
	Events []XidEvent `json:"events"`
	Since  string     `json:"since"`
	errors []health.ErroredMeasurement
}

// ErroredXid returns an XidMeasurement marked errored for a whole-query
// failure (the journal could not be read at all). Pass
// [MeasurementSignals]… for failedSignals so every Xid check that
// applies fails Critical rather than running CEL against an empty
// measurement. since records the window that was attempted.
func ErroredXid(since string, err error, failedSignals ...string) XidMeasurement {
	if len(failedSignals) == 0 {
		failedSignals = measurementSignals
	}
	m := XidMeasurement{Since: since}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement.
// err must be non-nil and is surfaced in the resulting verdict's
// message via its Error() string.
func (m *XidMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (XidMeasurement) Category() health.Category { return health.Xid }

// Errors implements [health.Measurement].
func (m XidMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
