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

func (*nativeLibrary) ReadGPUInventory(gpuIndices []uint) (map[uint]*observation.GPUInventoryEntry, error) {
	values, err := readGPUFields(gpuIndices, inventoryFields)
	if err != nil {
		return nil, err
	}
	return decodeGPUInventoryFields(gpuIndices, inventoryFields, values), nil
}

func (*nativeLibrary) ReadGPUUUIDs(gpuIndices []uint) (map[uint]GPUIdentityEntry, error) {
	values, err := readGPUFields(gpuIndices, []godcgm.Short{godcgm.DCGM_FI_DEV_UUID})
	if err != nil {
		return nil, err
	}
	return decodeGPUUUIDs(gpuIndices, values), nil
}

func decodeGPUUUIDs(gpuIndices []uint, values []godcgm.FieldValue_v2) map[uint]GPUIdentityEntry {
	byGPU := make(map[uint]*godcgm.FieldValue_v2, len(gpuIndices))
	for i := range values {
		field := &values[i]
		if field.EntityGroupId == godcgm.FE_GPU && field.FieldID == godcgm.DCGM_FI_DEV_UUID {
			byGPU[field.EntityID] = field
		}
	}
	identities := make(map[uint]GPUIdentityEntry, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		var uuid string
		var failure *observation.CollectionError
		if field := byGPU[gpuIndex]; field != nil {
			uuid, failure = decodeGPUString(field)
		} else {
			missing := fieldErrorFromStatus(godcgm.DCGM_ST_NO_DATA, fmt.Sprintf("DCGM returned no UUID for GPU index %d", gpuIndex))
			failure = &missing
		}
		if failure != nil {
			failure.Source = Name
			failure.Operation = "identity"
		}
		identities[gpuIndex] = GPUIdentityEntry{Index: gpuIndex, UUID: uuid, UUIDError: failure}
	}
	return identities
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

// Maps DCGM field identifiers to portable GpuDevice field names.
var inventoryFieldNames = map[godcgm.Short][]string{
	godcgm.DCGM_FI_DEV_UUID:                           {"uuid"},
	godcgm.DCGM_FI_DEV_NAME:                           {"model"},
	godcgm.DCGM_FI_DEV_SERIAL:                         {"serial"},
	godcgm.DCGM_FI_DEV_PCI_BUSID:                      {"bus_id"},
	godcgm.DCGM_FI_DEV_BRAND:                          {"brand"},
	godcgm.DCGM_FI_DEV_VBIOS_VERSION:                  {"vbios_version"},
	godcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER: {"chassis_serial"},
	godcgm.DCGM_FI_DEV_MINOR_NUMBER:                   {"minor_number"},
	godcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID:            {"fabric_cluster_uuid"},
	godcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID:               {"fabric_clique_id"},
	godcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY:        {"compute_capability_major", "compute_capability_minor"},
	godcgm.DCGM_FI_DEV_FB_TOTAL:                       {"framebuffer_total_bytes"},
	godcgm.DCGM_FI_DRIVER_VERSION:                     {"driver_version"},
	godcgm.DCGM_FI_CUDA_DRIVER_VERSION:                {"cuda_driver_version"},
}

func decodeGPUInventoryFields(gpuIndices []uint, requested []godcgm.Short, values []godcgm.FieldValue_v2) map[uint]*observation.GPUInventoryEntry {
	byGPU := make(map[uint]map[godcgm.Short]*godcgm.FieldValue_v2, len(gpuIndices))
	for i := range values {
		field := &values[i]
		if field.EntityGroupId != godcgm.FE_GPU {
			continue
		}
		if byGPU[field.EntityID] == nil {
			byGPU[field.EntityID] = make(map[godcgm.Short]*godcgm.FieldValue_v2)
		}
		byGPU[field.EntityID][field.FieldID] = field
	}
	entries := make(map[uint]*observation.GPUInventoryEntry, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		gpu := &observation.GPUDevice{}
		entry := &observation.GPUInventoryEntry{
			SourceIndex: uint32(gpuIndex),
			Gpu:         gpu,
		}
		for _, fieldID := range requested {
			field, exists := byGPU[gpuIndex][fieldID]
			var failure *observation.CollectionError
			if !exists {
				missing := fieldErrorFromStatus(godcgm.DCGM_ST_NO_DATA, fmt.Sprintf("DCGM returned no inventory field %d for GPU index %d", fieldID, gpuIndex))
				failure = &missing
			} else {
				failure = applyInventoryField(gpu, field)
			}
			if failure != nil {
				failure.Source = Name
				failure.Operation = "inventory"
				if entry.FieldErrors == nil {
					entry.FieldErrors = make(map[string]*observation.CollectionError)
				}
				for _, name := range inventoryFieldNames[fieldID] {
					entry.FieldErrors[name] = failure
				}
			}
		}
		entries[gpuIndex] = entry
	}
	return entries
}

func validateGPUField(field *godcgm.FieldValue_v2) *observation.CollectionError {
	if field.Status != godcgm.DCGM_ST_OK {
		failure := fieldErrorFromStatus(field.Status, fmt.Sprintf("DCGM field %d status %d", field.FieldID, field.Status))
		return &failure
	}
	value := godcgm.FieldValue_v1{FieldType: field.FieldType, Value: field.Value}
	if field.StringValue != nil {
		value.Value = [4096]byte{}
		copy(value.Value[:], *field.StringValue)
	}
	if sentinel, found := checkSentinel(value); found {
		failure := fieldErrorFromSentinel(sentinel)
		return &failure
	}
	return nil
}

func decodeGPUString(field *godcgm.FieldValue_v2) (string, *observation.CollectionError) {
	if failure := validateGPUField(field); failure != nil {
		return "", failure
	}
	if field.FieldType != godcgm.DCGM_FT_STRING {
		return "", &observation.CollectionError{
			Category: observation.CollectionErrorCategoryParseError,
			Detail:   fmt.Sprintf("DCGM field %d has an invalid type for a string", field.FieldID),
		}
	}
	var text string
	if field.StringValue != nil {
		text = *field.StringValue
	} else {
		text = field.String()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		failure := fieldErrorFromStatus(godcgm.DCGM_ST_NO_DATA, fmt.Sprintf("DCGM field %d is empty", field.FieldID))
		return "", &failure
	}
	return text, nil
}

func applyInventoryField(gpu *observation.GPUDevice, field *godcgm.FieldValue_v2) *observation.CollectionError {
	var textTarget **string
	switch field.FieldID {
	case godcgm.DCGM_FI_DEV_UUID:
		textTarget = &gpu.Uuid
	case godcgm.DCGM_FI_DEV_NAME:
		textTarget = &gpu.Model
	case godcgm.DCGM_FI_DEV_SERIAL:
		textTarget = &gpu.Serial
	case godcgm.DCGM_FI_DEV_PCI_BUSID:
		textTarget = &gpu.BusId
	case godcgm.DCGM_FI_DEV_BRAND:
		textTarget = &gpu.Brand
	case godcgm.DCGM_FI_DEV_VBIOS_VERSION:
		textTarget = &gpu.VbiosVersion
	case godcgm.DCGM_FI_DEV_PLATFORM_CHASSIS_SERIAL_NUMBER:
		textTarget = &gpu.ChassisSerial
	case godcgm.DCGM_FI_DEV_FABRIC_CLUSTER_UUID:
		textTarget = &gpu.FabricClusterUuid
	case godcgm.DCGM_FI_DRIVER_VERSION:
		textTarget = &gpu.DriverVersion
	}
	if textTarget != nil {
		text, failure := decodeGPUString(field)
		if failure != nil {
			return failure
		}
		*textTarget = &text
		return nil
	}
	if failure := validateGPUField(field); failure != nil {
		return failure
	}
	if field.FieldType == godcgm.DCGM_FT_INT64 {
		integer := field.Int64()
		switch field.FieldID {
		case godcgm.DCGM_FI_DEV_MINOR_NUMBER:
			if integer >= 0 && integer < godcgm.DCGM_FT_INT64_BLANK {
				gpu.MinorNumber = &integer
				return nil
			}
		case godcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID:
			if integer >= 0 && uint64(integer) <= uint64(^uint32(0)) {
				cliqueID := uint32(integer)
				gpu.FabricCliqueId = &cliqueID
				return nil
			}
		case godcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY:
			if integer > 0 && uint64(integer) <= uint64(^uint32(0)) {
				major, minor := uint32(uint64(integer)>>16), uint32(uint64(integer)&0xffff)
				gpu.ComputeCapabilityMajor, gpu.ComputeCapabilityMinor = &major, &minor
				return nil
			}
		case godcgm.DCGM_FI_DEV_FB_TOTAL:
			const bytesPerMiB = 1024 * 1024
			if integer > 0 && uint64(integer) <= ^uint64(0)/bytesPerMiB {
				bytes := uint64(integer) * bytesPerMiB
				gpu.FramebufferTotalBytes = &bytes
				return nil
			}
		case godcgm.DCGM_FI_CUDA_DRIVER_VERSION:
			if integer > 0 && integer < godcgm.DCGM_FT_INT64_BLANK {
				version := fmt.Sprintf("%d.%d", integer/1000, (integer%1000)/10)
				gpu.CudaDriverVersion = &version
				return nil
			}
		}
	}
	return &observation.CollectionError{
		Category: observation.CollectionErrorCategoryParseError,
		Detail:   fmt.Sprintf("DCGM inventory field %d has an invalid type or value", field.FieldID),
	}
}
