// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	nvmllib "github.com/dsx-ai-factory/health-validation/collect/source/nvml/lib"
)

type shutdownLibrary struct {
	nvmllib.Library
	shutdowns int
}

func (l *shutdownLibrary) Shutdown() nvml.Return { l.shutdowns++; return nvml.SUCCESS }

func TestNativeLifecycleGate(t *testing.T) {
	lib := &shutdownLibrary{}
	inst := &instance{nvmlLib: lib}
	other := &instance{}
	err := inst.Read(func(map[string]device.Device) error {
		assert.ErrorIs(t, other.Read(func(map[string]device.Device) error { require.FailNow(t, "entered native read twice"); return nil }), ErrInUse)
		assert.ErrorIs(t, inst.Refresh(), ErrInUse)
		assert.ErrorIs(t, inst.Shutdown(), ErrInUse)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 0, lib.shutdowns)
	for range 2 {
		require.NoError(t, inst.Shutdown())
	}
	require.Equal(t, 1, lib.shutdowns)
	require.ErrorIs(t, inst.Read(func(map[string]device.Device) error { require.FailNow(t, "read closed instance"); return nil }), ErrClosed)
	require.ErrorIs(t, inst.Refresh(), ErrClosed)
}
