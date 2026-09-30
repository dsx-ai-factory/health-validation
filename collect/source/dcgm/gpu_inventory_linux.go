//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"fmt"
	"strings"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

var inventoryFields = []godcgm.Short{
	godcgm.DCGM_FI_DEV_UUID,
	godcgm.DCGM_FI_DEV_NAME,
	godcgm.DCGM_FI_DEV_SERIAL,
	godcgm.DCGM_FI_DEV_PCI_BUSID,
	godcgm.DCGM_FI_DEV_BRAND,
	godcgm.DCGM_FI_DEV_VBIOS_VERSION,
	godcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER,
	godcgm.DCGM_FI_DEV_MINOR_NUMBER,
	godcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID,
	godcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID,
	godcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY,
	godcgm.DCGM_FI_DEV_FB_TOTAL,
	godcgm.DCGM_FI_DRIVER_VERSION,
	godcgm.DCGM_FI_CUDA_DRIVER_VERSION,
}

func (*nativeLibrary) ReadGPUInventory(gpuIndices []uint) (map[uint]*observation.GPUDevice, error) {
	values, err := readGPUFields(gpuIndices, inventoryFields)
	if err != nil {
		return nil, err
	}
	return decodeGPUInventory(gpuIndices, values), nil
}

func (*nativeLibrary) ReadGPUUUIDs(gpuIndices []uint) (map[uint]string, error) {
	values, err := readGPUFields(gpuIndices, []godcgm.Short{godcgm.DCGM_FI_DEV_UUID})
	if err != nil {
		return nil, err
	}
	return decodeGPUUUIDs(values), nil
}

func decodeGPUUUIDs(values []godcgm.FieldValue_v2) map[uint]string {
	gpuUUIDs := make(map[uint]string, len(values))
	for _, field := range values {
		if field.EntityGroupId != godcgm.FE_GPU || field.FieldID != godcgm.DCGM_FI_DEV_UUID || field.Status != godcgm.DCGM_ST_OK {
			continue
		}
		if gpuUUID, ok := inventoryString(field); ok {
			gpuUUIDs[field.EntityID] = gpuUUID
		}
	}
	return gpuUUIDs
}

func readGPUFields(gpuIndices []uint, fields []godcgm.Short) ([]godcgm.FieldValue_v2, error) {
	entities := make([]godcgm.GroupEntityPair, len(gpuIndices))
	for position, gpuIndex := range gpuIndices {
		entities[position] = godcgm.GroupEntityPair{EntityGroupId: godcgm.FE_GPU, EntityId: gpuIndex}
	}
	// UUID and inventory fields are not watched, so the cache may have no sample.
	values, err := godcgm.EntitiesGetLatestValues(entities, fields, godcgm.DCGM_FV_FLAG_LIVE_DATA)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}
	return values, nil
}

func decodeGPUInventory(gpuIndices []uint, values []godcgm.FieldValue_v2) map[uint]*observation.GPUDevice {
	gpus := make(map[uint]*observation.GPUDevice, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		gpus[gpuIndex] = &observation.GPUDevice{}
	}
	for _, field := range values {
		if field.EntityGroupId != godcgm.FE_GPU || field.Status != godcgm.DCGM_ST_OK {
			continue
		}
		gpu := gpus[field.EntityID]
		if gpu == nil {
			continue
		}
		if value, ok := inventoryString(field); ok {
			switch field.FieldID {
			case godcgm.DCGM_FI_DEV_UUID:
				gpu.Uuid = &value
			case godcgm.DCGM_FI_DEV_NAME:
				gpu.Model = &value
			case godcgm.DCGM_FI_DEV_SERIAL:
				gpu.Serial = &value
			case godcgm.DCGM_FI_DEV_PCI_BUSID:
				gpu.BusId = &value
			case godcgm.DCGM_FI_DEV_BRAND:
				gpu.Brand = &value
			case godcgm.DCGM_FI_DEV_VBIOS_VERSION:
				gpu.VbiosVersion = &value
			case godcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER:
				gpu.ChassisSerial = &value
			case godcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID:
				gpu.FabricClusterUuid = &value
			case godcgm.DCGM_FI_DRIVER_VERSION:
				gpu.DriverVersion = &value
			}
			continue
		}
		value, ok := inventoryInteger(field)
		if !ok {
			continue
		}
		switch field.FieldID {
		case godcgm.DCGM_FI_DEV_MINOR_NUMBER:
			if value >= 0 {
				gpu.MinorNumber = &value
			}
		case godcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID:
			if value >= 0 && uint64(value) <= uint64(^uint32(0)) {
				cliqueID := uint32(value)
				gpu.FabricCliqueId = &cliqueID
			}
		case godcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY:
			if value > 0 {
				major := uint32(uint64(value) >> 16)
				minor := uint32(uint64(value) & 0xffff)
				gpu.ComputeCapabilityMajor = &major
				gpu.ComputeCapabilityMinor = &minor
			}
		case godcgm.DCGM_FI_DEV_FB_TOTAL:
			const bytesPerMiB = 1024 * 1024
			if value > 0 && uint64(value) <= ^uint64(0)/bytesPerMiB {
				bytes := uint64(value) * bytesPerMiB
				gpu.FramebufferTotalBytes = &bytes
			}
		case godcgm.DCGM_FI_CUDA_DRIVER_VERSION:
			if value > 0 {
				version := fmt.Sprintf("%d.%d", value/1000, (value%1000)/10)
				gpu.CudaDriverVersion = &version
			}
		}
	}
	return gpus
}

func inventoryString(field godcgm.FieldValue_v2) (string, bool) {
	if field.FieldType != godcgm.DCGM_FT_STRING {
		return "", false
	}
	value := strings.TrimSpace(field.String())
	if value == "" || strings.HasPrefix(value, "<<<") && strings.HasSuffix(value, ">>>") {
		return "", false
	}
	return value, true
}

func inventoryInteger(field godcgm.FieldValue_v2) (int64, bool) {
	if field.FieldType != godcgm.DCGM_FT_INT64 {
		return 0, false
	}
	value := field.Int64()
	if value >= godcgm.DCGM_FT_INT64_BLANK {
		return 0, false
	}
	return value, true
}
