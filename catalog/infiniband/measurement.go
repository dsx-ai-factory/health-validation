// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband defines the measurement type and checks for
// InfiniBand / RDMA network health derived from the system journal. The
// mlx5 driver logs RDMA interface link transitions ("Link up"/"Link
// down") to the kernel journal; a sensor pairs each down with its next
// up, and the catalog evaluates the resulting outage durations against
// the link-flap checks.
package infiniband

import "github.com/dsx-ai-factory/health-validation/catalog/health"

// Link-flap signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in checks
// that consume LinkFlapMeasurements.
const (
	SignalOutageDurations = "outage_durations"
	SignalFlapEvents      = "flap_events"
	SignalSince           = "since"
)

// MeasurementSignals lists every signal a LinkFlapMeasurement carries.
// Sensors pass it as the failedSignals argument when they cannot query
// the journal at all, telling the catalog that no signal is trustworthy
// for this measurement.
var MeasurementSignals = []string{
	SignalOutageDurations,
	SignalFlapEvents,
	SignalSince,
}

// LinkFlapEvent is one RDMA interface outage: a "Link down" paired with
// the next "Link up" on the same interface. DurationSeconds is the wall
// time the link stayed down. Timestamps are the raw journal timestamps
// (RFC 3339-ish, second granularity) preserved for message and audit.
type LinkFlapEvent struct {
	Interface       string `json:"interface"`
	DownAt          string `json:"down_at"`
	UpAt            string `json:"up_at"`
	DurationSeconds int64  `json:"duration_seconds"`
}

// LinkFlapMeasurement is the aggregate of every RDMA link outage observed
// in the window described by Since. One measurement is produced per sensor
// pass, even when no outages were found (empty OutageDurations), so a
// check always runs and can report PASS.
//
// OutageDurations is the headline signal for catalog rules: it holds the
// down→up outage length in seconds for every paired outage, and a check
// counts how many meet or exceed its min_outage_seconds threshold.
// FlapEvents preserves the per-outage detail (interface and timestamps)
// for message interpolation and observability.
type LinkFlapMeasurement struct {
	Since           string                      `json:"since"`
	OutageDurations []int64                     `json:"outage_durations"`
	FlapEvents      []LinkFlapEvent             `json:"flap_events"`
	errors          []health.ErroredMeasurement `json:"-"`
}

// ErroredLinkFlap returns a LinkFlapMeasurement marked errored for a
// whole-query failure (the journal could not be read at all). Pass
// [MeasurementSignals]… for failedSignals so every link-flap check fails
// Critical rather than running CEL against an empty measurement. since
// records the window that was attempted.
func ErroredLinkFlap(since string, err error, failedSignals ...string) LinkFlapMeasurement {
	m := LinkFlapMeasurement{Since: since}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends one collection-failure entry to the measurement. err
// must be non-nil and is surfaced in the resulting verdict's message via
// its Error() string.
func (m *LinkFlapMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (LinkFlapMeasurement) Category() health.Category { return health.InfiniBand }

// Errors implements [health.Measurement].
func (m LinkFlapMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
