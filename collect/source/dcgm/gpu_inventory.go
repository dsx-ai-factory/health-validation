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
	return source.newCollectionReader(currentCycle).gpuIdentities(ctx)
}

// gpuIdentities preserves enumerated IDs when enrichment fails.
func (reader *collectionReader) gpuIdentities(ctx context.Context) ([]GPUIdentityEntry, *observation.CollectionError) {
	gpuIndices, failure := reader.gpuIndices(ctx)
	if failure != nil {
		return nil, failure
	}
	return reader.gpuUUIDs(ctx, gpuIndices), nil
}

// gpuIndices discovers IDs for subsequent reads on the same session.
func (reader *collectionReader) gpuIndices(ctx context.Context) ([]uint, *observation.CollectionError) {
	gpuIndices, err := runNative(ctx, reader, func(context.Context, *clientSession) ([]uint, error) {
		return reader.source.native.AllGPUIndices()
	})
	if err != nil {
		return nil, reader.source.gpuCollectionError("identity", err)
	}
	return gpuIndices, nil
}

// gpuUUIDs preserves request order and reports UUID and PCI failures independently.
func (reader *collectionReader) gpuUUIDs(ctx context.Context, gpuIndices []uint) []GPUIdentityEntry {
	if len(gpuIndices) == 0 {
		return nil
	}
	// Native work can outlive a timeout, so it must not retain caller-owned IDs.
	gpuIndices = slices.Clone(gpuIndices)
	source := reader.source
	identities, readErr := runNative(ctx, reader, func(_ context.Context, session *clientSession) (map[uint]GPUIdentityEntry, error) {
		entries, err := source.native.ReadGPUUUIDs(gpuIndices)
		for _, entry := range entries {
			session.recordFieldFailure(reader.cycle, entry.UUIDError)
			session.recordFieldFailure(reader.cycle, entry.BusIDError)
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
// Use Collect to keep discovery and inventory on the same session.
func (source *Source) GPUInventory(ctx context.Context, currentCycle *collect.Cycle, identities []GPUIdentityEntry) *observation.GPUInventorySnapshot {
	return source.newCollectionReader(currentCycle).gpuInventory(ctx, identities)
}

// gpuInventory adds device attributes while preserving known identity and status.
func (reader *collectionReader) gpuInventory(ctx context.Context, identities []GPUIdentityEntry) *observation.GPUInventorySnapshot {
	gpuIndices := make([]uint, 0, len(identities))
	for _, identity := range identities {
		gpuIndices = append(gpuIndices, identity.Index)
	}
	var err error
	var collected map[uint]*observation.GPUInventoryEntry
	source := reader.source
	if len(gpuIndices) > 0 {
		collected, err = runNative(ctx, reader, func(_ context.Context, session *clientSession) (map[uint]*observation.GPUInventoryEntry, error) {
			entries, readErr := source.native.ReadGPUInventory(gpuIndices)
			for _, entry := range entries {
				session.recordFieldFailure(reader.cycle, entry.GetCollectionError())
				for _, failure := range entry.GetFieldErrors() {
					session.recordFieldFailure(reader.cycle, failure)
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
