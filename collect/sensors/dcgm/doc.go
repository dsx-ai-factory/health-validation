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
//
// When health is selected, Measure makes one health read. Consumers needing
// the same findings can share the returned batch.
//
// Metrics and health findings retain native DCGM entity IDs and attempt GPU
// UUID enrichment after collection. Missing UUIDs never discard collected
// values. Native IDs are source-local and may change after a restart; consumers
// that need persistent GPU identity must require a UUID. Metric targets retain
// their existing source_local_id fallback when the UUID is unavailable.
//
// Health observations target the node and retain native incident entities and
// the native overall result for the complete group. An empty incident list
// means no incidents were reported; it does not assert that every target was
// checked or healthy. Successful native reports are available immediately,
// including empty incident lists. MonitoredSince records when this Source
// enabled monitoring; consumers choose any startup grace period.
// NO_TARGETS means no selected system has relevant GPU or NVSwitch targets.
// Usable reports retain missing-target facts alongside incidents.
// Report members and incidents use shared GPU and NVSwitch references with
// native identity metadata; unknown native entity kinds remain available.
// These are source findings; consumers supply verdict and recovery policy.
// Consumers must use an observation schema that includes DcgmHealthReport
// before opting into health collection.
//
// # Implementation
//
// sensor.go defines the shared sensor, New, and the Measure flow, including
// metric observations and optional inventory. health.go adds NewWithHealth and
// health-report handling to that sensor. signals.go holds supported metric
// definitions and the metric resolver shared by both constructors.
package dcgm
