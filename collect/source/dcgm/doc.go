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
// timeouts, and connection failures remain shared. WatchPolicy errors detected
// at construction affect the whole Source, including health registration.
// Metric field groups are prepared only when metrics are read. Field-mapping
// errors found during that setup leave inventory and health available.
//
// Collect independently selects metrics, health, and inventory and returns their
// raw results and partial errors. Selecting nothing performs no native work.
// A collection binds to the current client session on its first native operation.
// Many collections reuse that session and may interleave their native operations.
// Each later operation checks the same session under the native-call gate;
// a failed or replaced session cannot supply more data to that collection.
// Completed results remain available. Optional health identity enrichment may
// fail without discarding the native report. Sensors format the composed result
// into observations rather than orchestrating separate Source reads.
//
// Requested health systems may have no targets when hardware is discovered;
// reports list those systems separately in SystemsWithoutTargets.
// Each health read refreshes GPU and NVSwitch discovery, using active GPUs,
// and repairs missing members in the owned group. Settings are reapplied for added
// members; unchanged membership reuses the current watches. General GPU
// inventory can still include lost or detached devices that health cannot watch.
//
// A failed initialization is shared with same-cycle Source calls until another
// cycle attempts initialization. Collect retains its own admission error for
// its entire request, including while an overlapping collection retries.
// Retrying does not close the Source or change its registrations.
//
// Runtime health setup failures retry on the next read. Unfinished group cleanup
// retains ownership and must succeed before replacement allocation. Discovery
// and rejected GPU or NVSwitch additions appear in SystemErrors alongside
// findings from usable members, even when every discovery call fails. These
// errors indicate incomplete coverage, not absent targets. Rejected additions
// retry on the next read without resetting usable members' history. If all
// additions fail without usable members, the read fails and cleans up the
// empty group. Connection loss, missing groups, or failures enabling health
// systems invalidate the shared setup.
//
// Confirmed connection loss invalidates the client session for every collection
// already using it. A fresh collection in a later cycle may replace the client
// and both watches; existing collections keep their original session. A timed-out
// native call must finish before recovery can enter DCGM. Recovery requires
// this Source to be the process's sole go-dcgm owner: another outstanding Init
// reference keeps the wrapper's old shared connection alive.
//
// Collect performs one fresh health read when selected. Reports retain the
// collection ID.
// PossiblyTruncated marks a report that reaches the native incident limit;
// absent incidents cannot establish that omitted faults have cleared.
//
// GPUIdentities combines discovery and identity lookup on one session. UUIDs and
// PCI addresses are fetched together; either field can succeed independently.
// The existing GPUInventory and ReadGPUMetrics methods are standalone reads;
// supplied IDs and identities must belong to the current session. Use Collect
// to join metrics, health, and inventory safely across concurrent recovery.
// UUIDs identify GPUs across collections. PCI addresses describe their location
// on this node and can help correlate hardware replacements. Native numeric IDs
// are source-local and must not be treated as persistent hardware identities.
//
// Source HealthWatch options configure health sampling independently of WatchPolicy.
// Nil options or zero fields retain DCGM's HealthSet defaults: a requested
// 30-second update interval and 10-minute retention. Set HealthWatchOptions on
// StandaloneOptions or EmbeddedOptions before constructing the Source. Use an
// interval matching the health polling cadence and MaxKeepAge covering
// the longest expected gap between polls. These options do not enable health;
// register the desired systems separately. Native systems can enforce different
// effective settings. Requested retention does not establish complete history
// for every health rule, including a memory rule that uses a
// week-old baseline. Collect returns successful native health reports immediately.
// MonitoredSince records the group's first successful health setup. Systems
// or members added later can have less history. Elapsed time does not establish
// native coverage. Consumers choose any startup grace period.
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
// If Close returns ErrInUse, retry after the native call finishes. Other cleanup
// errors consume their resources and do not require another Close.
package dcgm
