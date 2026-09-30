//go:build !linux || !cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import "github.com/dsx-ai-factory/health-validation/collect/observation"

type unsupportedNative struct{}

func newNative(StandaloneOptions) nativeAPI { return unsupportedNative{} }

func (unsupportedNative) Init() (func(), error)                { return nil, unsupportedError() }
func (unsupportedNative) SupportedGPUIndices() ([]uint, error) { return nil, unsupportedError() }
func (unsupportedNative) ReadGPUUUIDs([]uint) (map[uint]string, error) {
	return nil, unsupportedError()
}

func (unsupportedNative) ReadGPUInventory([]uint) (map[uint]*observation.GPUDevice, error) {
	return nil, unsupportedError()
}

func (unsupportedNative) WatchMetrics([]string) (func() error, error) {
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
