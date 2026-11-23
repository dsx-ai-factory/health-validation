// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// GPUIdentityEntry pairs a DCGM index with optional UUID and PCI address enrichment.
type GPUIdentityEntry struct {
	UUIDError  *observation.CollectionError
	BusIDError *observation.CollectionError
	UUID       string
	BusID      string
	Index      uint
}

// GPUIdentities enumerates GPUs and enriches their indices with UUIDs and PCI addresses.
// Lookup failures preserve the indices for subsequent collection.
func (source *Source) GPUIdentities(ctx context.Context, currentCycle *collect.Cycle) ([]GPUIdentityEntry, *observation.CollectionError) {
	gpuIndices, failure := source.GPUIndices(ctx, currentCycle)
	if failure != nil {
		return nil, failure
	}
	return source.GPUUUIDs(ctx, currentCycle, gpuIndices), nil
}

// GPUIndices enumerates source-local GPU IDs without looking up UUIDs.
func (source *Source) GPUIndices(ctx context.Context, currentCycle *collect.Cycle) ([]uint, *observation.CollectionError) {
	gpuIndices, err := runNative(ctx, currentCycle, source, func(context.Context) ([]uint, error) {
		return source.native.AllGPUIndices()
	})
	if err != nil {
		return nil, source.gpuCollectionError("identity", err)
	}
	return gpuIndices, nil
}

// GPUUUIDs enriches the supplied GPU IDs with UUIDs and PCI addresses in one
// lookup without enumerating devices. It preserves request order and IDs,
// recording failures independently for each identity field.
// An empty request performs no native work.
func (source *Source) GPUUUIDs(ctx context.Context, currentCycle *collect.Cycle, gpuIndices []uint) []GPUIdentityEntry {
	if len(gpuIndices) == 0 {
		return nil
	}
	// Native work can outlive a timeout, so it must not retain caller-owned IDs.
	gpuIndices = slices.Clone(gpuIndices)
	identities, readErr := runNative(ctx, currentCycle, source, func(context.Context) (map[uint]GPUIdentityEntry, error) {
		entries, err := source.native.ReadGPUUUIDs(gpuIndices)
		for _, entry := range entries {
			source.recordFieldFailure(currentCycle, entry.UUIDError)
			source.recordFieldFailure(currentCycle, entry.BusIDError)
		}
		return entries, err
	})
	entries := make([]GPUIdentityEntry, 0, len(gpuIndices))
	for _, gpuIndex := range gpuIndices {
		entry := identities[gpuIndex]
		entry.Index = gpuIndex
		if entry.UUID == "" && entry.UUIDError == nil {
			err := readErr
			if err == nil {
				err = fmt.Errorf("no UUID returned for GPU index %d", gpuIndex)
			}
			entry.UUIDError = source.gpuCollectionError("identity", err)
		}
		if entry.BusID == "" && entry.BusIDError == nil {
			err := readErr
			if err == nil {
				err = fmt.Errorf("no PCI bus ID returned for GPU index %d", gpuIndex)
			}
			entry.BusIDError = source.gpuCollectionError("identity", err)
		}
		entries = append(entries, entry)
	}
	return entries
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
		collected, err = runNative(ctx, currentCycle, source, func(context.Context) (map[uint]*observation.GPUInventoryEntry, error) {
			entries, readErr := source.native.ReadGPUInventory(gpuIndices)
			for _, entry := range entries {
				source.recordFieldFailure(currentCycle, entry.GetCollectionError())
				for _, failure := range entry.GetFieldErrors() {
					source.recordFieldFailure(currentCycle, failure)
				}
			}
			return entries, readErr
		})
	}
	entries := make([]*observation.GPUInventoryEntry, 0, len(gpuIndices))
	for _, identity := range identities {
		gpuIndex := identity.Index
		entry := collected[gpuIndex]
		if entry == nil {
			entry = &observation.GPUInventoryEntry{}
			if err == nil {
				entry.CollectionError = source.gpuCollectionError("inventory", fmt.Errorf("no inventory returned for GPU index %d", gpuIndex))
			}
		}
		entry.SourceIndex = uint32(gpuIndex)
		if entry.Gpu == nil {
			entry.Gpu = &observation.GPUDevice{}
		}
		// Preserve the GPU status if reading its attributes also failed.
		if err != nil && entry.CollectionError == nil {
			entry.CollectionError = source.gpuCollectionError("inventory", err)
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
