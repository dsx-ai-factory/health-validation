//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"encoding/binary"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

func TestDecodeGPUUUIDs(t *testing.T) {
	field := func(gpuIndex uint, fieldID godcgm.Short, value string) godcgm.FieldValue_v2 {
		result := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: gpuIndex, FieldID: fieldID, FieldType: godcgm.DCGM_FT_STRING, Status: godcgm.DCGM_ST_OK}
		copy(result.Value[:], value)
		return result
	}
	badStatus := field(2, godcgm.DCGM_FI_DEV_UUID, "GPU-2")
	badStatus.Status = godcgm.DCGM_ST_GENERIC_ERROR
	gpuUUIDs := decodeGPUUUIDs([]godcgm.FieldValue_v2{
		field(1, godcgm.DCGM_FI_DEV_UUID, "GPU-1"),
		field(1, godcgm.DCGM_FI_DEV_NAME, "H100"),
		badStatus,
		field(3, godcgm.DCGM_FI_DEV_UUID, "<<<Not Found>>>"),
	})
	if len(gpuUUIDs) != 1 || gpuUUIDs[1] != "GPU-1" {
		t.Fatalf("decodeGPUUUIDs() = %v, want only GPU-1", gpuUUIDs)
	}
}

func TestDecodeInventory(t *testing.T) {
	stringField := func(gpuIndex uint, fieldID godcgm.Short, value string) godcgm.FieldValue_v2 {
		field := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: gpuIndex, FieldID: fieldID, FieldType: godcgm.DCGM_FT_STRING, Status: godcgm.DCGM_ST_OK}
		copy(field.Value[:], value)
		return field
	}
	integerField := func(gpuIndex uint, fieldID godcgm.Short, value int64) godcgm.FieldValue_v2 {
		field := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: gpuIndex, FieldID: fieldID, FieldType: godcgm.DCGM_FT_INT64, Status: godcgm.DCGM_ST_OK}
		binary.LittleEndian.PutUint64(field.Value[:], uint64(value))
		return field
	}
	fields := []godcgm.FieldValue_v2{
		stringField(7, godcgm.DCGM_FI_DEV_UUID, "GPU-7"),
		stringField(7, godcgm.DCGM_FI_DEV_NAME, "H100"),
		stringField(7, godcgm.DCGM_FI_DEV_PCI_BUSID, "0000:53:00.0"),
		integerField(7, godcgm.DCGM_FI_DEV_MINOR_NUMBER, 0),
		integerField(7, godcgm.DCGM_FI_DEV_FABRIC_CLIQUE_ID, 0),
		integerField(7, godcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY, 9<<16),
		integerField(7, godcgm.DCGM_FI_DEV_FB_TOTAL, 81920),
		integerField(7, godcgm.DCGM_FI_CUDA_DRIVER_VERSION, 12080),
		stringField(8, godcgm.DCGM_FI_DEV_UUID, "<<<Not Found>>>"),
	}
	gpus := decodeGPUInventory([]uint{7, 8}, fields)
	gpu := gpus[7]
	if gpu.GetUuid() != "GPU-7" || gpu.GetModel() != "H100" || gpu.GetBusId() != "0000:53:00.0" {
		t.Fatalf("identity fields: %+v", gpu)
	}
	if gpu.MinorNumber == nil || *gpu.MinorNumber != 0 || gpu.FabricCliqueId == nil || *gpu.FabricCliqueId != 0 {
		t.Fatalf("zero-valued optional fields were lost: %+v", gpu)
	}
	if gpu.GetComputeCapabilityMajor() != 9 || gpu.GetComputeCapabilityMinor() != 0 || gpu.GetFramebufferTotalBytes() != 81920*1024*1024 || gpu.GetCudaDriverVersion() != "12.8" {
		t.Fatalf("derived fields: %+v", gpu)
	}
	if gpus[8].Uuid != nil {
		t.Fatalf("DCGM sentinel was retained: %+v", gpus[8])
	}
}
