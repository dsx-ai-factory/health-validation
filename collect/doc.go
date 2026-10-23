// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package collect is the shared collection library: source clients,
// sensors, and the observation types they emit.
//
// This package is experimental (v0) and outside the project's support
// lifecycle. Its contracts follow the shared-collection-library proposal and
// may change. Nothing in this package applies health judgment; it collects and
// normalizes only.
//
// Raw families cover GPU inventory and telemetry, memory reliability, NVLink
// and fabric, GPU processes, Linux processes, host memory/CPU/resources,
// block devices and filesystems, FUSE connections, PCI, and pstore records.
// NVML GPM profiling is opt-in and requires a sample interval greater than
// 100 milliseconds. Existing DCGM, InfiniBand, and kernel-message adapters
// remain separate sources. See docs/integration.md in the repository for
// constructors, custom server registration, and source access boundaries.
//
// Sensor observations use typed targets, including boot-qualified processes,
// GPU processes, block devices, namespace-qualified mounts, PCI addresses,
// FUSE connections, path/hash-qualified pstore records, and journal entries by
// journald boot ID and cursor. Unsigned counters
// use Value.uint_value; protobuf JSON retains uint64 precision as decimal
// strings. A sysfs counter reporting "N/A (no PMA)" produces NoValue, while
// a failed file read produces CollectionError. Consumers must inspect the
// outcome rather than interpreting missing values as healthy readings.
//
// File-backed sensors accept root-relative fs.FS views; nil uses the live
// filesystem root. Process metadata defaults to comm, never environ, and
// command-line diagnostics require explicit opt-in. Filesystem capacity skips
// unsafe mounts. Pstore timestamps describe collection, not the crash time.
// No sensor owns scheduling, remediation, or historical utilization state.
package collect
