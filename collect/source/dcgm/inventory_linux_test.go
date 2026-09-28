//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"encoding/binary"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

func TestDecodeInventory(t *testing.T) {
	stringField := func(id uint, fieldID godcgm.Short, value string) godcgm.FieldValue_v2 {
		field := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: id, FieldID: fieldID, FieldType: godcgm.DCGM_FT_STRING, Status: godcgm.DCGM_ST_OK}
		copy(field.Value[:], value)
		return field
	}
	integerField := func(id uint, fieldID godcgm.Short, value int64) godcgm.FieldValue_v2 {
		field := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: id, FieldID: fieldID, FieldType: godcgm.DCGM_FT_INT64, Status: godcgm.DCGM_ST_OK}
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
	devices := decodeInventory([]uint{7, 8}, fields)
	device := devices[7]
	if device.GetId() != "GPU-7" || device.GetModel() != "H100" || device.GetPciBusId() != "0000:53:00.0" {
		t.Fatalf("identity fields: %+v", device)
	}
	if device.MinorNumber == nil || *device.MinorNumber != 0 || device.FabricCliqueId == nil || *device.FabricCliqueId != 0 {
		t.Fatalf("zero-valued optional fields were lost: %+v", device)
	}
	if device.GetComputeCapabilityMajor() != 9 || device.GetComputeCapabilityMinor() != 0 || device.GetFramebufferTotalBytes() != 81920*1024*1024 || device.GetCudaDriverVersion() != "12.8" {
		t.Fatalf("derived fields: %+v", device)
	}
	if devices[8].Id != nil {
		t.Fatalf("DCGM sentinel was retained: %+v", devices[8])
	}
}
