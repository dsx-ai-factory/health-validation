// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package dcgm manages the DCGM client, metric and health watches, native-call safety, and
// conversion of DCGM failures into collection errors. A source either connects
// to a running nv-hostengine (standalone) or starts DCGM inside the process
// (embedded); a process uses one mode.
//
// Keep one Source open for the process lifetime. Register all required metric
// signals and health systems before collection starts, then read repeatedly and Close
// when finished. Native initialization and watches remain lazy. Health setup
// and metric setup have independent failure state within the same client
// lifetime; native calls, timeouts, and connection failures remain shared.
// Requested health systems may have no targets when hardware is discovered;
// reports list those systems separately in SystemsWithoutTargets.
//
// ReadHealth performs a fresh read on each call. When health is selected,
// sensors call it once per measurement. CheckId is the collection ID.
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
// MonitoredSince records when this Source enabled health monitoring; elapsed
// time does not establish native coverage. Consumers choose any startup grace
// period.
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
