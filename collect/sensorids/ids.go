// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package sensorids defines sensor identifiers without collector dependencies.
package sensorids

const (
	// NVML identifies the NVML-backed GPU sensor.
	NVML = "gpu.nvml"
	// DCGM identifies the DCGM-backed GPU sensor.
	DCGM = "gpu.dcgm"
)
