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

// Measurement is the interface all domain measurement types implement.
//
// Each measurement carries data for one entity (e.g. one GPU) in one
// domain (e.g. GPU health). The catalog uses Category to find
// applicable checks. Errors reports any per-signal collection failures
// the sensor encountered while building the measurement; the catalog
// fails a check whose RequiredSignals overlap with any error's
// FailedSignals and lets every other check run normally against the
// fields the sensor did populate.
type Measurement interface {
	// Category returns the hardware domain this measurement belongs to.
	Category() Category

	// Errors returns the list of per-signal collection failures the
	// sensor encountered, or nil/empty if the entity was fully
	// observable. Each entry names one or more signals that could
	// not be collected together with a human-readable detail; the
	// catalog inspects this list per check to decide whether to fail
	// or proceed with CEL evaluation against partial data.
	Errors() []ErroredMeasurement
}

// ErroredMeasurement records one per-signal collection failure on a
// measurement. Sensors append entries via the typed measurement's
// AddError method (e.g. [GPUMeasurement.AddError]) as they encounter
// failures while measuring different fields, so a single measurement
// may carry several entries — one per failing call.
//
// The catalog fails a check Critical when any entry's FailedSignals
// overlaps with the check's RequiredSignals; the verdict's message
// joins the matching entries' Err.Error() strings. Checks whose
// required signals were all collected still run normally on the
// partial data.
//
// The zero value is not valid; construct entries with
// [NewErroredMeasurement] (or via the typed measurement's AddError),
// which guarantees Err is non-nil so consumers always have a
// surfaceable failure cause.
type ErroredMeasurement struct {
	err           error
	failedSignals []string
}

// NewErroredMeasurement constructs an [ErroredMeasurement]. err must
// be non-nil — every entry carries a surfaceable failure cause that
// the catalog renders into the resulting CheckVerdict's Message.
// Passing nil is a programming error and panics.
func NewErroredMeasurement(err error, failedSignals ...string) ErroredMeasurement {
	if err == nil {
		panic("health: ErroredMeasurement requires non-nil Err")
	}
	return ErroredMeasurement{err: err, failedSignals: failedSignals}
}

// Err returns the underlying sensor error that prevented the listed
// signals from being collected. It is guaranteed non-nil for any
// ErroredMeasurement obtained through the public API. Its Error()
// string is what the catalog surfaces in the resulting CheckVerdict's
// Message; consumers that need structured details (e.g. an NVML
// Return code) can type-assert on the concrete error type the sensor
// produced.
func (m ErroredMeasurement) Err() error { return m.err }

// FailedSignals returns the signal names (matching the keys emitted
// into the signal map by the measurement's struct tags) that this
// entry's failure prevented the sensor from collecting. For
// whole-entity failures the entry includes every signal in the
// measurement schema; for per-field failures, only the specific
// signals the failing call would have populated.
func (m ErroredMeasurement) FailedSignals() []string { return m.failedSignals }
