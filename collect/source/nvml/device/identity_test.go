// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package device

import (
	"testing"

	nvlibdevice "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

type identityDevice struct{ nvlibdevice.Device }

func (d *identityDevice) GetUUID() (string, nvml.Return) {
	panic("resolved identity constructor must not query UUID")
}

func TestNewWithIdentityPreservesUnresolvedAndDriverGate(t *testing.T) {
	dev := NewWithIdentity(&identityDevice{}, "", "0000:01:00.0", WithDriverMajor(535))
	require.Empty(t, dev.UUID())
	require.Equal(t, "0000:01:00.0", dev.PCIBusID())
	_, ret := dev.GetMarginTemperature()
	require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
}
