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

// Package xid defines the measurement type for NVRM Xid errors parsed
// from the system journal. Xid errors are GPU faults the NVIDIA driver
// logs to the kernel ring buffer / syslog; a sensor collects them and
// the catalog evaluates the aggregate against GPU checks.
package xid

import "github.com/NVIDIA/health-validation/catalog/health"

// Xid signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in checks
// that consume Xid measurements.
const (
	SignalEvents     = "events"
	SignalXidCounts  = "xid_counts"
	SignalSince      = "since"
	SignalEventCount = "event_count"
)

// MeasurementSignals lists every signal an XidMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot query
// the journal at all, telling the catalog that no signal is
// trustworthy for this measurement.
var MeasurementSignals = []string{
	SignalEvents,
	SignalXidCounts,
	SignalSince,
	SignalEventCount,
}

// XidEvent is a single parsed "NVRM: Xid …" log line. Pid and
// ProcessName are populated only when the driver attributed the fault
// to a process; some Xid lines carry neither.
type XidEvent struct {
	ProcessName string `json:"process_name"`
	PCIAddress  string `json:"pci_address"`
	XidCode     int64  `json:"xid_code"`
	Pid         int64  `json:"pid"`
}

// XidMeasurement is the aggregate of every Xid event observed in the
// window described by Since. One measurement is produced per sensor
// pass, even when no events were found (EventCount == 0), so a check
// always runs and can report PASS.
//
// XidCounts maps each Xid code to the number of times it occurred and
// is the headline signal for catalog rules (e.g. xid_counts[79] > 0).
// Events preserves the full per-occurrence detail — process name, PCI
// address, and pid — for message interpolation and richer rules.
type XidMeasurement struct {
	XidCounts  map[int64]int64 `json:"xid_counts"`
	Since      string          `json:"since"`
	Events     []XidEvent      `json:"events"`
	errors     []health.ErroredMeasurement
	EventCount int64 `json:"event_count"`
}

// ErroredXid returns an XidMeasurement marked errored for a whole-query
// failure (the journal could not be read at all). Pass
// [MeasurementSignals]… for failedSignals so every Xid check that
// applies fails Critical rather than running CEL against an empty
// measurement. since records the window that was attempted.
func ErroredXid(since string, err error, failedSignals ...string) XidMeasurement {
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
