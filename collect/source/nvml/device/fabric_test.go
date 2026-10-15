// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package device

import (
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestReadFabricInfoDriverGateAndFallback(t *testing.T) {
	v1 := nvml.GpuFabricInfo{ClusterUuid: [16]uint8{1, 2, 3}, Status: uint32(nvml.ERROR_NOT_READY), CliqueId: 42, State: 3}
	v3 := nvml.GpuFabricInfo_v3{ClusterUuid: [16]uint8{4, 5, 6}, Status: uint32(nvml.SUCCESS), CliqueId: 43, State: 3, HealthMask: 0x55, HealthSummary: 1}
	tests := []struct {
		name   string
		driver int
		v3ret  nvml.Return
		v1ret  nvml.Return
		wantV3 int
		wantV1 int
		base   nvml.Return
		health nvml.Return
	}{
		{name: "old driver skips V3", driver: 535, wantV1: 1, health: nvml.ERROR_NOT_SUPPORTED},
		{name: "unknown driver skips V3", driver: 0, wantV1: 1, health: nvml.ERROR_NOT_SUPPORTED},
		{name: "V3 boundary supported", driver: 550, wantV3: 1},
		{name: "unsupported V3 falls back", driver: 550, v3ret: nvml.ERROR_NOT_SUPPORTED, wantV3: 1, wantV1: 1, health: nvml.ERROR_NOT_SUPPORTED},
		{name: "missing V3 falls back", driver: 570, v3ret: nvml.ERROR_FUNCTION_NOT_FOUND, wantV3: 1, wantV1: 1, health: nvml.ERROR_FUNCTION_NOT_FOUND},
		{name: "version mismatch falls back", driver: 570, v3ret: nvml.ERROR_ARGUMENT_VERSION_MISMATCH, wantV3: 1, wantV1: 1, health: nvml.ERROR_ARGUMENT_VERSION_MISMATCH},
		{name: "permission does not fall back", driver: 570, v3ret: nvml.ERROR_NO_PERMISSION, wantV3: 1, base: nvml.ERROR_NO_PERMISSION, health: nvml.ERROR_NO_PERMISSION},
		{name: "lost GPU does not fall back", driver: 570, v3ret: nvml.ERROR_GPU_IS_LOST, wantV3: 1, base: nvml.ERROR_GPU_IS_LOST, health: nvml.ERROR_GPU_IS_LOST},
		{name: "reset does not fall back", driver: 570, v3ret: nvml.ERROR_RESET_REQUIRED, wantV3: 1, base: nvml.ERROR_RESET_REQUIRED, health: nvml.ERROR_RESET_REQUIRED},
		{name: "timeout does not fall back", driver: 570, v3ret: nvml.ERROR_TIMEOUT, wantV3: 1, base: nvml.ERROR_TIMEOUT, health: nvml.ERROR_TIMEOUT},
		{name: "unknown error does not fall back", driver: 570, v3ret: nvml.ERROR_UNKNOWN, wantV3: 1, base: nvml.ERROR_UNKNOWN, health: nvml.ERROR_UNKNOWN},
		{name: "old driver V1 failure", driver: 535, v1ret: nvml.ERROR_NO_PERMISSION, wantV1: 1, base: nvml.ERROR_NO_PERMISSION, health: nvml.ERROR_NO_PERMISSION},
		{name: "fallback V1 lost", driver: 570, v3ret: nvml.ERROR_NOT_SUPPORTED, v1ret: nvml.ERROR_GPU_IS_LOST, wantV3: 1, wantV1: 1, base: nvml.ERROR_GPU_IS_LOST, health: nvml.ERROR_GPU_IS_LOST},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			callsV3, callsV1 := 0, 0
			info, base, health := readFabricInfo(test.driver, func() (nvml.GpuFabricInfo_v3, nvml.Return) { callsV3++; return v3, test.v3ret }, func() (nvml.GpuFabricInfo, nvml.Return) { callsV1++; return v1, test.v1ret })
			require.Equal(t, test.wantV3, callsV3)
			require.Equal(t, test.wantV1, callsV1)
			require.Equal(t, test.base, base)
			require.Equal(t, test.health, health)
			if base != nvml.SUCCESS {
				require.Equal(t, nvml.GpuFabricInfo_v3{}, info)
				return
			}
			if callsV1 == 0 {
				require.Equal(t, v3, info)
			} else {
				require.Equal(t, v1.ClusterUuid, info.ClusterUuid)
				require.Equal(t, v1.Status, info.Status)
				require.Equal(t, v1.CliqueId, info.CliqueId)
				require.Equal(t, v1.State, info.State)
				require.Zero(t, info.HealthMask)
				require.Zero(t, info.HealthSummary)
				require.NotEqual(t, nvml.SUCCESS, health)
			}
		})
	}
}
