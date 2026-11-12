//go:build !linux || !cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import "github.com/dsx-ai-factory/health-validation/collect/observation"

type unsupportedNative struct{}

func newNative(StandaloneOptions, sessionMode) nativeAPI { return unsupportedNative{} }

func (unsupportedNative) MetricWatchGroups([]string) ([]metricWatchGroup, error) {
	return []metricWatchGroup{}, nil
}

func (unsupportedNative) Init() (func(), error)          { return nil, unsupportedError() }
func (unsupportedNative) AllGPUIndices() ([]uint, error) { return nil, unsupportedError() }
func (unsupportedNative) ReadGPUUUIDs([]uint) (map[uint]GPUIdentityEntry, error) {
	return nil, unsupportedError()
}

func (unsupportedNative) ReadGPUInventory([]uint) (map[uint]*observation.GPUInventoryEntry, error) {
	return nil, unsupportedError()
}

func (unsupportedNative) WatchMetrics([]metricWatchGroup) (func() error, error) {
	return nil, unsupportedError()
}

func (unsupportedNative) ReadGPUMetrics(uint, []string) (map[string]FieldValue, error) {
	return nil, unsupportedError()
}

func unsupportedError() error {
	return &dcgmError{
		category: observation.CollectionErrorCategoryUnsupported,
		detail:   "DCGM collection requires Linux with cgo enabled",
	}
}
