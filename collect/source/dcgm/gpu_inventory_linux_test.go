//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestReadGPUInventoryPreservesStatusAndQueryErrors(t *testing.T) {
	for _, queryFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("query_fails_%t", queryFails), func(t *testing.T) {
			indices := []uint{2, 7, 9}
			var checked []uint
			library := &nativeLibrary{
				gpuStatus: func(gpuIndex uint) godcgm.EntityStatus {
					checked = append(checked, gpuIndex)
					switch gpuIndex {
					case 7:
						return godcgm.EntityStatusLost
					case 9:
						return godcgm.EntityStatusUnknown
					default:
						return godcgm.EntityStatusOk
					}
				},
				readFields: func(gpuIndices []uint, fields []godcgm.Short) ([]godcgm.FieldValue_v2, error) {
					if !slices.Equal(gpuIndices, indices) || !slices.Equal(fields, inventoryFields) {
						t.Fatalf("unexpected read: %v, %v", gpuIndices, fields)
					}
					if queryFails {
						return nil, &dcgmError{sourceError: "DCGM_ST_NO_PERMISSION", category: observation.CollectionErrorCategoryPermissionDenied, detail: "No permission"}
					}
					model := "H100"
					return []godcgm.FieldValue_v2{{EntityGroupId: godcgm.FE_GPU, EntityID: 7, FieldID: godcgm.DCGM_FI_DEV_NAME, FieldType: godcgm.DCGM_FT_STRING, StringValue: &model}}, nil
				},
			}
			entries, err := library.ReadGPUInventory(indices)
			if err != nil || len(entries) != len(indices) || !slices.Equal(checked, indices) {
				t.Fatalf("membership or checks lost: %v, %v, %v", entries, checked, err)
			}
			for _, gpuIndex := range indices {
				entry := entries[gpuIndex]
				if entry.GetSourceIndex() != uint32(gpuIndex) {
					t.Fatalf("index lost: %+v", entry)
				}
				failure := entry.GetCollectionError()
				if gpuIndex == 7 {
					if failure.GetSourceError() != "DcgmEntityStatusLost" || failure.GetCategory() != observation.CollectionErrorCategoryUnavailable {
						t.Fatalf("Lost status replaced: %v", failure)
					}
				} else if queryFails {
					if failure.GetSourceError() != "DCGM_ST_NO_PERMISSION" || failure.GetCategory() != observation.CollectionErrorCategoryPermissionDenied {
						t.Fatalf("query failure hidden by status: %v", failure)
					}
				} else if gpuIndex == 9 {
					if failure.GetSourceError() != "DcgmEntityStatusUnknown" {
						t.Fatalf("unknown status lost: %v", failure)
					}
				} else if failure != nil {
					t.Fatalf("healthy status has an error: %v", failure)
				}
				if failure != nil && (failure.GetSource() != Name || failure.GetOperation() != "inventory") {
					t.Fatalf("error attribution lost: %v", failure)
				}
			}
			if !queryFails && entries[7].GetGpu().GetModel() != "H100" {
				t.Fatalf("Lost GPU's available attributes discarded: %+v", entries[7])
			}
		})
	}
}

func TestReadGPUInventoryAllLost(t *testing.T) {
	indices := []uint{2, 7}
	library := &nativeLibrary{
		gpuStatus: func(uint) godcgm.EntityStatus { return godcgm.EntityStatusLost },
		readFields: func([]uint, []godcgm.Short) ([]godcgm.FieldValue_v2, error) {
			return nil, errors.New("attributes unavailable")
		},
	}
	entries, err := library.ReadGPUInventory(indices)
	if err != nil || len(entries) != len(indices) {
		t.Fatalf("all-Lost membership discarded: %v, %v", entries, err)
	}
	for _, gpuIndex := range indices {
		if entries[gpuIndex].GetCollectionError().GetSourceError() != "DcgmEntityStatusLost" {
			t.Fatalf("Lost error discarded: %v", entries[gpuIndex])
		}
	}
}

func TestGPUInventoryStatusError(t *testing.T) {
	cases := []struct {
		sourceError string
		status      godcgm.EntityStatus
		category    observation.CollectionErrorCategory
	}{
		{status: godcgm.EntityStatusOk},
		{status: godcgm.EntityStatusFake},
		{status: godcgm.EntityStatusLost, sourceError: "DcgmEntityStatusLost", category: observation.CollectionErrorCategoryUnavailable},
		{status: godcgm.EntityStatusUnknown, sourceError: "DcgmEntityStatusUnknown", category: observation.CollectionErrorCategoryUnavailable},
		{status: godcgm.EntityStatusUnsupported, sourceError: "DcgmEntityStatusUnsupported", category: observation.CollectionErrorCategoryUnsupported},
		{status: godcgm.EntityStatusInaccessible, sourceError: "DcgmEntityStatusInaccessible", category: observation.CollectionErrorCategoryUnavailable},
		{status: godcgm.EntityStatusDisabled, sourceError: "DcgmEntityStatusDisabled", category: observation.CollectionErrorCategoryUnavailable},
		{status: godcgm.EntityStatusDetached, sourceError: "DcgmEntityStatusDetached", category: observation.CollectionErrorCategoryUnavailable},
	}
	for _, test := range cases {
		t.Run(test.status.String(), func(t *testing.T) {
			failure := gpuInventoryStatusError(7, test.status)
			if test.sourceError == "" {
				if failure != nil {
					t.Fatalf("healthy status has an error: %v", failure)
				}
			} else if failure.GetSourceError() != test.sourceError || failure.GetCategory() != test.category {
				t.Fatalf("status error = %v", failure)
			}
		})
	}
}

func TestDecodeGPUUUIDs(t *testing.T) {
	field := func(gpuIndex uint, fieldID godcgm.Short, value string) godcgm.FieldValue_v2 {
		result := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: gpuIndex, FieldID: fieldID, FieldType: godcgm.DCGM_FT_STRING, Status: godcgm.DCGM_ST_OK}
		copy(result.Value[:], value)
		return result
	}
	badStatus := field(2, godcgm.DCGM_FI_DEV_UUID, "GPU-2")
	badStatus.Status = godcgm.DCGM_ST_GENERIC_ERROR
	otherEntity := field(4, godcgm.DCGM_FI_DEV_UUID, "GPU-4")
	otherEntity.EntityGroupId = godcgm.FE_NONE
	gpuUUIDs := decodeGPUUUIDs([]uint{1, 2, 3, 4}, []godcgm.FieldValue_v2{
		field(1, godcgm.DCGM_FI_DEV_UUID, "GPU-1"),
		field(1, godcgm.DCGM_FI_DEV_NAME, "H100"),
		badStatus,
		field(3, godcgm.DCGM_FI_DEV_UUID, godcgm.DCGM_FT_STR_NOT_FOUND),
		otherEntity,
		field(9, godcgm.DCGM_FI_DEV_UUID, "GPU-9"),
	})
	if len(gpuUUIDs) != 4 || gpuUUIDs[1].UUID != "GPU-1" || gpuUUIDs[1].UUIDError != nil || gpuUUIDs[2].UUIDError == nil || gpuUUIDs[3].UUIDError.GetSourceError() != "DCGM_FT_NOT_FOUND" || gpuUUIDs[4].UUIDError.GetSourceError() != "DCGM_ST_NO_DATA" {
		t.Fatalf("decodeGPUUUIDs() = %v, want GPU-1 and errors for GPUs 2, 3 and 4", gpuUUIDs)
	}
	for _, gpuIndex := range []uint{2, 3, 4} {
		entry := gpuUUIDs[gpuIndex]
		if entry.Index != gpuIndex || entry.UUID != "" || entry.UUIDError.GetSource() != Name || entry.UUIDError.GetOperation() != "identity" {
			t.Fatalf("UUID failure lost its index or attribution: %+v", entry)
		}
	}
}

func TestDecodeGPUUUIDStringValues(t *testing.T) {
	cases := []struct {
		name      string
		value     string
		uuid      string
		category  string
		fieldType uint
	}{
		{name: "trim whitespace", value: " GPU-1 ", uuid: "GPU-1", fieldType: godcgm.DCGM_FT_STRING},
		{name: "empty", value: " \t", category: "COLLECTION_ERROR_CATEGORY_UNAVAILABLE", fieldType: godcgm.DCGM_FT_STRING},
		{name: "sentinel", value: godcgm.DCGM_FT_STR_NOT_SUPPORTED, category: "COLLECTION_ERROR_CATEGORY_UNSUPPORTED", fieldType: godcgm.DCGM_FT_STRING},
		{name: "wrong type", category: "COLLECTION_ERROR_CATEGORY_PARSE_ERROR", fieldType: godcgm.DCGM_FT_INT64},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			field := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: 1, FieldID: godcgm.DCGM_FI_DEV_UUID, FieldType: test.fieldType, StringValue: &test.value}
			entry := decodeGPUUUIDs([]uint{1}, []godcgm.FieldValue_v2{field})[1]
			if entry.UUID != test.uuid {
				t.Fatalf("UUID = %q, want %q", entry.UUID, test.uuid)
			}
			if test.category == "" {
				if entry.UUIDError != nil {
					t.Fatalf("unexpected UUID error: %v", entry.UUIDError)
				}
			} else if entry.UUIDError.GetCategory().String() != test.category {
				t.Fatalf("UUID error = %v, want %s", entry.UUIDError, test.category)
			}
		})
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
		stringField(8, godcgm.DCGM_FI_DEV_UUID, godcgm.DCGM_FT_STR_NOT_FOUND),
	}
	gpus := decodeGPUInventoryFields([]uint{7, 8}, inventoryFields, fields)
	gpu := gpus[7].GetGpu()
	if gpu.GetModel() != "H100" || gpu.GetBusId() != "0000:53:00.0" {
		t.Fatalf("identity fields: %+v", gpu)
	}
	if gpu.MinorNumber == nil || *gpu.MinorNumber != 0 || gpu.FabricCliqueId == nil || *gpu.FabricCliqueId != 0 {
		t.Fatalf("zero-valued optional fields were lost: %+v", gpu)
	}
	if gpu.GetComputeCapabilityMajor() != 9 || gpu.GetComputeCapabilityMinor() != 0 || gpu.GetFramebufferTotalBytes() != 81920*1024*1024 || gpu.GetCudaDriverVersion() != "12.8" {
		t.Fatalf("derived fields: %+v", gpu)
	}
	if gpu.Uuid != nil || gpus[8].GetGpu().Uuid != nil {
		t.Fatalf("DCGM sentinel was retained: %+v", gpus[8])
	}
}

func TestDecodeInventoryPreservesFieldFailures(t *testing.T) {
	textField := func(fieldID godcgm.Short, value string) godcgm.FieldValue_v2 {
		field := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: 7, FieldID: fieldID, FieldType: godcgm.DCGM_FT_STRING}
		copy(field.Value[:], value)
		return field
	}
	cases := []struct {
		name        string
		value       string
		sourceError string
		category    string
		status      int
	}{
		{name: "unsupported status", status: godcgm.DCGM_ST_NOT_SUPPORTED, sourceError: "DCGM_ST_NOT_SUPPORTED", category: "COLLECTION_ERROR_CATEGORY_UNSUPPORTED"},
		{name: "permission", status: godcgm.DCGM_ST_NO_PERMISSION, sourceError: "DCGM_ST_NO_PERMISSION", category: "COLLECTION_ERROR_CATEGORY_PERMISSION_DENIED"},
		{name: "blank", value: godcgm.DCGM_FT_STR_BLANK, sourceError: "DCGM_FT_BLANK", category: "COLLECTION_ERROR_CATEGORY_UNAVAILABLE"},
		{name: "unsupported sentinel", value: godcgm.DCGM_FT_STR_NOT_SUPPORTED, sourceError: "DCGM_FT_NOT_SUPPORTED", category: "COLLECTION_ERROR_CATEGORY_UNSUPPORTED"},
		{name: "empty", sourceError: "DCGM_ST_NO_DATA", category: "COLLECTION_ERROR_CATEGORY_UNAVAILABLE"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			serial := textField(godcgm.DCGM_FI_DEV_SERIAL, test.value)
			serial.Status = test.status
			entries := decodeGPUInventoryFields([]uint{7}, inventoryFields, []godcgm.FieldValue_v2{textField(godcgm.DCGM_FI_DEV_NAME, "H100"), serial})
			entry := entries[7]
			if entry.GetGpu().GetModel() != "H100" || entry.GetGpu().Serial != nil {
				t.Fatalf("partial attributes = %+v", entry)
			}
			failure := entry.FieldErrors["serial"]
			if failure.GetSourceError() != test.sourceError || failure.GetCategory().String() != test.category || failure.GetSource() != Name || failure.GetOperation() != "inventory" {
				t.Fatalf("serial error = %+v", failure)
			}
			if missing := entry.FieldErrors["bus_id"]; missing.GetSourceError() != "DCGM_ST_NO_DATA" {
				t.Fatalf("omitted field error = %+v", missing)
			}
			if entry.FieldErrors["uuid"] != nil {
				t.Fatal("enrichment must not read or report UUID")
			}
		})
	}
}

func TestDecodeInventoryMalformedAndNumericSentinels(t *testing.T) {
	malformed := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: 1, FieldID: godcgm.DCGM_FI_DEV_NAME, FieldType: godcgm.DCGM_FT_INT64}
	capability := godcgm.FieldValue_v2{EntityGroupId: godcgm.FE_GPU, EntityID: 1, FieldID: godcgm.DCGM_FI_DEV_CUDA_COMPUTE_CAPABILITY, FieldType: godcgm.DCGM_FT_INT64}
	binary.LittleEndian.PutUint64(capability.Value[:], uint64(godcgm.DCGM_FT_INT64_NOT_SUPPORTED))
	entry := decodeGPUInventoryFields([]uint{1}, inventoryFields, []godcgm.FieldValue_v2{malformed, capability})[1]
	if entry.FieldErrors["model"].GetCategory().String() != "COLLECTION_ERROR_CATEGORY_PARSE_ERROR" {
		t.Fatalf("type mismatch = %+v", entry.FieldErrors["model"])
	}
	for _, name := range []string{"compute_capability_major", "compute_capability_minor"} {
		if entry.FieldErrors[name].GetSourceError() != "DCGM_FT_NOT_SUPPORTED" {
			t.Fatalf("numeric sentinel = %+v", entry.FieldErrors[name])
		}
	}
}
