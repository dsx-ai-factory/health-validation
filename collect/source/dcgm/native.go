// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

// nativeAPI keeps platform-specific DCGM calls behind a small internal
// interface. The unsupported implementation returns a collection error.
type nativeAPI interface {
	Init() (func(), error)
	SupportedDevices() ([]uint, error)
	DeviceUUID(uint) (string, error)
	WatchMetrics([]string) (func() error, error)
	ReadMetrics(uint, []string) (map[string]FieldValue, error)
}
