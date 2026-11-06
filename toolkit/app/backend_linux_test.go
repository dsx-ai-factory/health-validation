// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package app

import (
	"slices"
	"testing"

	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
)

// The shared NVML sensor offers every NVML signal DCGM does not supply: the
// stock CLI's process signals, and others such as ECC that only a caller
// adapter requests.
func TestNVMLGPUSignalsAreNVMLMinusDCGM(t *testing.T) {
	ids := nvmlGPUSignals.IDs()
	for _, id := range dcgmGPUSignals.IDs() {
		if slices.Contains(ids, id) {
			t.Errorf("%s is declared by both the DCGM and NVML built-ins", id)
		}
	}
	for _, id := range append(processGPUSignals.IDs(), gpusignals.ECCDoubleBitVolatileTotal.ID(), gpusignals.RowRemapPending.ID()) {
		if !slices.Contains(ids, id) {
			t.Errorf("NVML built-in does not declare %s", id)
		}
	}
}
