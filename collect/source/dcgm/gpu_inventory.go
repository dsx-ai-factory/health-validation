// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// GPUIdentityEntry pairs a DCGM index with optional UUID enrichment.
type GPUIdentityEntry struct {
	UUIDError *observation.CollectionError
	UUID      string
	Index     uint
}

// GPUIdentities enumerates GPUs and enriches their indices with UUIDs.
// A UUID lookup failure preserves the indices for subsequent collection.
func (source *Source) GPUIdentities(ctx context.Context, currentCycle *collect.Cycle) ([]GPUIdentityEntry, *observation.CollectionError) {
	gpuIndices, err := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) ([]uint, error) {
		if startErr := source.startNativeCall(); startErr != nil {
			return nil, startErr
		}
		defer source.finishNativeCall()
		if initErr := source.ensureInitialized(callContext); initErr != nil {
			return nil, initErr
		}
		if contextErr := callContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		return source.native.AllGPUIndices()
	})
	if err != nil {
		return nil, source.gpuCollectionError("identity", err)
	}
	if len(gpuIndices) == 0 {
		return nil, nil
	}
	// Keep membership outside this call so a UUID timeout cannot discard it.
	identities, readErr := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) (map[uint]GPUIdentityEntry, error) {
		if startErr := source.startNativeCall(); startErr != nil {
			return nil, startErr
		}
		defer source.finishNativeCall()
		if initErr := source.ensureInitialized(callContext); initErr != nil {
			return nil, initErr
		}
		if contextErr := callContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		return source.native.ReadGPUUUIDs(gpuIndices)
	})
	entries := make([]GPUIdentityEntry, 0, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		entry := identities[gpuIndex]
		entry.Index = gpuIndex
		if readErr != nil {
			entry.UUIDError = source.gpuCollectionError("identity", readErr)
		} else if entry.UUID == "" && entry.UUIDError == nil {
			entry.UUIDError = source.gpuCollectionError("identity", fmt.Errorf("no UUID returned for GPU index %d", gpuIndex))
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// GPUInventory enriches the supplied identities without re-enumerating GPUs or reading UUIDs again.
func (source *Source) GPUInventory(ctx context.Context, currentCycle *collect.Cycle, identities []GPUIdentityEntry) *observation.GPUInventorySnapshot {
	gpuIndices := make([]uint, 0, len(identities))
	for _, identity := range identities {
		gpuIndices = append(gpuIndices, identity.Index)
	}
	var err error
	var collected map[uint]*observation.GPUInventoryEntry
	if len(gpuIndices) > 0 {
		collected, err = collect.Run(ctx, currentCycle, Name, func(callContext context.Context) (map[uint]*observation.GPUInventoryEntry, error) {
			if startErr := source.startNativeCall(); startErr != nil {
				return nil, startErr
			}
			defer source.finishNativeCall()
			if initErr := source.ensureInitialized(callContext); initErr != nil {
				return nil, initErr
			}
			if contextErr := callContext.Err(); contextErr != nil {
				return nil, contextErr
			}
			return source.native.ReadGPUInventory(gpuIndices)
		})
	}
	entries := make([]*observation.GPUInventoryEntry, 0, len(gpuIndices))
	for _, identity := range identities {
		gpuIndex := identity.Index
		entry := collected[gpuIndex]
		if err != nil || entry == nil {
			readErr := err
			if readErr == nil {
				readErr = fmt.Errorf("no inventory returned for GPU index %d", gpuIndex)
			}
			entry = &observation.GPUInventoryEntry{
				CollectionError: source.gpuCollectionError("inventory", readErr),
			}
		}
		entry.SourceIndex = uint32(gpuIndex)
		if entry.Gpu == nil {
			entry.Gpu = &observation.GPUDevice{}
		}
		if identity.UUID != "" {
			gpuUUID := identity.UUID
			entry.Gpu.Uuid = &gpuUUID
		} else if identity.UUIDError != nil {
			if entry.FieldErrors == nil {
				entry.FieldErrors = make(map[string]*observation.CollectionError)
			}
			entry.FieldErrors["uuid"] = identity.UUIDError
		}
		entries = append(entries, entry)
	}
	return &observation.GPUInventorySnapshot{
		Source: source.ID(), CollectedAt: timestamppb.Now(),
		Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{Entries: entries}},
	}
}

func (source *Source) gpuCollectionError(operation string, err error) *observation.CollectionError {
	failure := collectionError(err)
	failure.Source = source.ID()
	failure.Operation = operation
	return &failure
}
