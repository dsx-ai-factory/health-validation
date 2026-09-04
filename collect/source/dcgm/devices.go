// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// Device identifies one GPU returned by DCGM. Index remains available when
// DCGM cannot read the UUID, allowing the sensor to attribute that failure.
type Device struct {
	CollectionError *observation.CollectionError
	UUID            string
	Index           uint
}

// Devices enumerates GPU identities once for the calling sensor. A
// source-wide failure is returned separately; a failure for one GPU is
// attached to that device so it does not hide the remaining devices.
func (source *Source) Devices(
	ctx context.Context,
	currentCycle *collect.Cycle,
) ([]Device, *observation.CollectionError) {
	devices, err := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) ([]Device, error) {
		if err := source.startNativeCall(); err != nil {
			return nil, err
		}
		defer source.finishNativeCall()

		if err := source.ensureInitialized(); err != nil {
			return nil, err
		}
		deviceIDs, err := source.native.SupportedDevices()
		if err != nil {
			return nil, err
		}
		devices := make([]Device, 0, len(deviceIDs))
		for _, deviceID := range deviceIDs {
			if err := callContext.Err(); err != nil {
				return nil, err
			}
			device := Device{Index: deviceID}
			uuid, err := source.native.DeviceUUID(deviceID)
			if err != nil {
				failure := collectionError(err)
				devices = append(devices, Device{
					Index: deviceID, CollectionError: &failure,
				})
				continue
			}
			device.UUID = uuid
			devices = append(devices, device)
		}
		return devices, nil
	})
	if err != nil {
		failure := collectionError(err)
		return nil, &failure
	}
	return devices, nil
}
