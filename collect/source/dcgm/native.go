// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import "github.com/dsx-ai-factory/health-validation/collect/observation"

// nativeAPI keeps platform-specific DCGM calls behind a small internal
// interface. The unsupported implementation returns a collection error.
type nativeAPI interface {
	Init() (func(), error)
	AllGPUIndices() ([]uint, error)
	ReadGPUUUIDs([]uint) (map[uint]GPUIdentityEntry, error)
	ReadGPUInventory([]uint) (map[uint]*observation.GPUInventoryEntry, error)
	WatchMetrics([]string) (func() error, error)
	ReadGPUMetrics(uint, []string) (map[string]FieldValue, error)
}

// sessionMode selects how a source starts DCGM. The zero value is unset.
type sessionMode uint8

const (
	standaloneMode sessionMode = iota + 1
	embeddedMode
	autoMode
)

func (mode sessionMode) String() string {
	switch mode {
	case standaloneMode:
		return "standalone"
	case embeddedMode:
		return "embedded"
	case autoMode:
		return "auto"
	default:
		return "unknown"
	}
}

// nativeOptions configures the platform-specific DCGM client.
type nativeOptions struct {
	address         string
	groupNamePrefix string
	isSocket        bool
	mode            sessionMode
}
