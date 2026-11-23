// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgm converts NVIDIA DCGM fields and optional passive health reports
// into observations.
//
// Applications can use NewWithHealth for both metrics-only and combined
// collection: a zero systems mask skips health, and a nonzero mask requests
// the selected health checks. Only explicitly supplied metrics are collected;
// a nonzero mask with no metric IDs collects health only. Health results use
// one combined dcgm.health report.
//
// New retains the existing metrics-only API, including selecting every
// supported metric when no IDs are supplied. Both constructors return the
// same sensor implementation; an application uses one, not both, per sensor.
//
// Create a Source, construct all sensors sharing it, then call Measure with a
// collection cycle for each read. Native initialization and watch setup are
// lazy. Per-call signal selection filters output without reconfiguring watches.
// Close the Source when collection is finished.
// Measure passes the selected metric IDs, health, and inventory to one Source
// collection. Those paths remain independently selectable and share one client
// session for the measurement. Concurrent measurements may interleave native
// operations, but an existing measurement cannot follow a recovered session.
// Results completed before a session failure remain in its batch.
//
// When health is selected, Measure makes one health read. Consumers needing
// the same findings can share the returned batch.
//
// Metrics and health findings retain native DCGM entity IDs. Metrics look up
// GPU UUIDs and PCI addresses before reading fields; health reports attempt the
// same enrichment after the native health check. Either identity field can be
// available when the other fails. Missing UUIDs retain native identities, but a
// lookup timeout can prevent subsequent native reads. Native IDs are source-local
// and may change after a restart; consumers that need persistent GPU identity
// must require a UUID. Metric targets retain their existing source_local_id
// fallback when the UUID is unavailable.
//
// Health observations target the node and retain native incident entities and
// the native overall result for the complete group. An empty incident list
// means no incidents were reported; it does not assert that every target was
// checked or healthy. Successful native reports are available immediately,
// including empty incident lists. MonitoredSince records the group's first
// successful setup; systems enabled later may have less history. Consumers
// choose any startup grace period.
// NO_TARGETS means no selected system has relevant GPU or NVSwitch targets;
// this source-wide outcome has no observation target.
// Usable reports retain missing-target facts and per-system discovery errors
// alongside incidents. The native overall result covers the enabled group;
// it does not establish coverage for systems listed in SystemErrors.
// Report members and incidents use shared GPU and NVSwitch references with
// native identity metadata; unknown native entity kinds remain available.
// These are source findings; consumers supply verdict and recovery policy.
// Consumers must use an observation schema that includes DcgmHealthReport
// before opting into health collection.
//
// # Implementation
//
// sensor.go defines the shared sensor, New, and formatting of the Source's
// composed collection result. health.go adds NewWithHealth and health-report
// formatting to that sensor. signals.go holds supported metric
// definitions and the metric resolver shared by both constructors.
package dcgm
