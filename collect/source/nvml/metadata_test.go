// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"testing"

	nvlibdevice "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	nvmllib "github.com/dsx-ai-factory/health-validation/collect/source/nvml/lib"
)

type metadataAPI struct {
	nvml.Interface
	countReturn nvml.Return
}

func (m *metadataAPI) SystemGetDriverVersion() (string, nvml.Return) { return "580.1", nvml.SUCCESS }
func (m *metadataAPI) SystemGetCudaDriverVersion_v2() (int, nvml.Return) {
	return 0, nvml.ERROR_NO_PERMISSION
}
func (m *metadataAPI) DeviceGetCount() (int, nvml.Return) { return 3, m.countReturn }
func (m *metadataAPI) DeviceGetHandleByIndex(index int) (nvml.Device, nvml.Return) {
	if index == 1 {
		return nil, nvml.ERROR_GPU_IS_LOST
	}
	return &metadataNativeDevice{uuidFailed: index == 2}, nvml.SUCCESS
}

type metadataNativeDevice struct {
	nvml.Device
	uuidFailed bool
}

func (d *metadataNativeDevice) GetUUID() (string, nvml.Return) {
	if d.uuidFailed {
		return "", nvml.ERROR_NO_PERMISSION
	}
	return "healthy", nvml.SUCCESS
}

type metadataWrappedDevice struct {
	nvlibdevice.Device
	handle nvml.Device
}

func (d *metadataWrappedDevice) GetUUID() (string, nvml.Return) { return d.handle.GetUUID() }
func (d *metadataWrappedDevice) GetPCIBusID() (string, error)   { return "0000:01:00.0", nil }

type metadataDeviceLib struct{ nvlibdevice.Interface }

func (d *metadataDeviceLib) NewDevice(handle nvml.Device) (nvlibdevice.Device, error) {
	return &metadataWrappedDevice{handle: handle}, nil
}

type metadataLibrary struct {
	nvmllib.Library
	api *metadataAPI
}

func (l *metadataLibrary) NVML() nvml.Interface          { return l.api }
func (l *metadataLibrary) Device() nvlibdevice.Interface { return &metadataDeviceLib{} }

func TestMetadataRetainsIndependentReturns(t *testing.T) {
	inst := &instance{nvmlLib: &metadataLibrary{api: &metadataAPI{}}}
	err := inst.Read(func(map[string]device.Device) error {
		metadata := inst.Metadata()
		require.Equal(t, "580.1", metadata.DriverVersion)
		require.Equal(t, nvml.SUCCESS, metadata.DriverReturn)
		require.Equal(t, nvml.ERROR_NO_PERMISSION, metadata.CUDAReturn)
		return nil
	})
	require.NoError(t, err)
}

func TestEnumerationKeepsFailedSlotsAndHealthyDevices(t *testing.T) {
	devices, records, err := enumerateInventory(&metadataLibrary{api: &metadataAPI{}}, 580)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.Contains(t, devices, "healthy")
	require.Len(t, records, 3)
	require.Equal(t, uint32(1), records[1].SourceIndex)
	require.Nil(t, records[1].Device)
	require.Equal(t, nvml.ERROR_GPU_IS_LOST, records[1].HandleReturn)
	require.NotNil(t, records[2].Device)
	require.Equal(t, nvml.ERROR_NO_PERMISSION, records[2].UUIDReturn)
	require.Error(t, records[2].Error)
}

func TestEnumerationCountFailureIsSourceLevel(t *testing.T) {
	devices, records, err := enumerateInventory(&metadataLibrary{api: &metadataAPI{countReturn: nvml.ERROR_UNKNOWN}}, 580)
	require.Error(t, err)
	require.Nil(t, devices)
	require.Nil(t, records)
}
