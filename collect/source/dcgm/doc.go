// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgm manages the DCGM client, metric and health watches, native-call safety, and
// conversion of DCGM failures into collection errors. A source either connects
// to a running nv-hostengine (standalone) or starts DCGM inside the process
// (embedded); a process uses one mode.
//
// Keep one Source open for the process lifetime. Register all required metric
// signals and health systems before the first metric or health read. Inventory
// reads leave registration open. Then read repeatedly and Close when finished.
// Native initialization and watches remain lazy. Health and metric setup have
// independent failure state within the same client lifetime; native calls,
// timeouts, and connection failures remain shared.
// Metric field groups are prepared only when metrics are read. Field-mapping
// errors leave inventory and health collection available.
// Requested health systems may have no targets when hardware is discovered;
// reports list those systems separately in SystemsWithoutTargets.
//
// A failed client initialization is shared by subsequent DCGM calls in the
// same cycle. The next cycle may try initialization again without closing the
// Source or changing its registrations.
//
// Runtime health setup failures retry on the next read. Unfinished group cleanup
// retains ownership and must succeed before replacement allocation. Discovery
// failures for one entity kind can accompany findings from available systems
// in SystemErrors; an error does not establish that targets are absent.
// Adding group members or enabling systems affects the shared health group;
// failure rolls back the group and returns an error for that read.
//
// Confirmed connection loss returns an error in the failing collection and
// replaces the client and both watches lazily in a later cycle. A timed-out
// native call must finish before recovery can enter DCGM. Recovery requires
// this Source to be the process's sole go-dcgm owner: another outstanding Init
// reference keeps the wrapper's old shared connection alive.
//
// ReadHealth performs a fresh read on each call. When health is selected,
// sensors call it once per measurement. Reports retain the collection ID.
// PossiblyTruncated marks a report that reaches the native incident limit;
// absent incidents cannot establish that omitted faults have cleared.
//
// GPUIndices and GPUUUIDs separate device discovery from optional UUID lookup;
// GPUIdentities retains their combined convenience flow. UUIDs identify GPUs
// across collections. Native numeric IDs are source-local and must not be
// treated as persistent hardware identities.
//
// Passive health uses DCGM's HealthSet defaults, independently of WatchPolicy:
// a requested 30-second update interval and 10-minute retention. Native systems
// can enforce different effective settings. These defaults do not establish
// complete history for every health rule, including a memory rule that uses a
// week-old baseline. ReadHealth returns successful native reports immediately.
// MonitoredSince records the group's first successful health setup. Systems
// enabled later can have less history. Elapsed time does not establish native
// coverage. Consumers choose any startup grace period.
//
// WatchPolicy controls this client's metric sampling requests, not a node-wide
// rate limit. DCGM shares its cache per entity and field across clients and
// groups, using the fastest requested interval and longest requested history.
// Native health watches can overlap metrics: NVLink health's default interval
// can request faster sampling than a slower metric policy for the same counters.
// An owned group isolates the enabled health systems, not their sampling rate.
//
// Health cleanup follows dcgm-exporter's best-effort ownership: Close attempts
// to destroy the owned entity group once and releases the client reference.
// Native health sampling may persist in an external hostengine, including
// after the final client disconnects. Group ownership does not isolate health
// watches from other groups using the same native connection.
package dcgm
