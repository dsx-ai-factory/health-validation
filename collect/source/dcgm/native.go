// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import "github.com/dsx-ai-factory/health-validation/collect/observation"

// nativeAPI keeps platform-specific DCGM calls behind a small internal
// interface. The unsupported implementation returns a collection error.
type nativeAPI interface {
	// MetricWatchGroups validates and groups metric fields before WatchMetrics.
	MetricWatchGroups([]string) ([]metricWatchGroup, error)
	Init() (func() error, error)
	AllGPUIndices() ([]uint, error)
	ReadGPUUUIDs([]uint) (map[uint]GPUIdentityEntry, error)
	// ReadGPUInventory returns fresh entries owned by the caller for enrichment.
	ReadGPUInventory([]uint) (map[uint]*observation.GPUInventoryEntry, error)
	// WatchMetrics returns one-pass cleanup on success. On failure it attempts
	// rollback, joins setup and cleanup errors, and returns no cleanup function.
	WatchMetrics([]metricWatchGroup) (func() error, error)
	ReadGPUMetrics(uint, []string) (map[string]FieldValue, error)
	// WatchHealth constructs a configured watch; Read creates its native group
	// lazily and retains ownership when failed setup needs later cleanup.
	WatchHealth(uint64) (healthWatch, error)
}

// healthWatch manages an optional native health group separately from the shared client.
type healthWatch interface {
	// Read sets up monitoring lazily and returns the native result and coverage.
	Read() (*observation.DCGMHealthReport, error)
	// Close stops new reads and attempts group destruction without releasing the client.
	Close() error
}

// sessionMode selects how a source starts DCGM. The zero value is unset.
type sessionMode uint8

const (
	standaloneMode sessionMode = iota + 1
	embeddedMode
)

func (mode sessionMode) String() string {
	switch mode {
	case standaloneMode:
		return "standalone"
	case embeddedMode:
		return "embedded"
	default:
		return "unknown"
	}
}
