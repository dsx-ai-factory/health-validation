// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// GPUIdentityEntry pairs a DCGM index with its UUID or a per-GPU error.
type GPUIdentityEntry struct {
	CollectionError *observation.CollectionError
	UUID            string
	Index           uint
}

type gpuReadError struct {
	cause     error
	operation string
}

func (failure gpuReadError) Error() string { return failure.operation + ": " + failure.cause.Error() }
func (failure gpuReadError) Unwrap() error { return failure.cause }

// GPUIdentities enumerates GPUs and reads only their UUIDs for metric identity.
func (source *Source) GPUIdentities(ctx context.Context, currentCycle *collect.Cycle) ([]GPUIdentityEntry, *observation.CollectionError) {
	entries, err := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) ([]GPUIdentityEntry, error) {
		if err := source.startNativeCall(); err != nil {
			return nil, err
		}
		defer source.finishNativeCall()
		if err := source.ensureInitialized(); err != nil {
			return nil, err
		}
		gpuIndices, err := source.native.SupportedGPUIndices()
		if err != nil {
			return nil, fmt.Errorf("enumerate GPUs: %w", err)
		}
		if ctxErr := callContext.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if len(gpuIndices) == 0 {
			return nil, nil
		}
		gpuUUIDs, err := source.native.ReadGPUUUIDs(gpuIndices)
		if err != nil {
			return nil, gpuReadError{operation: "identity", cause: err}
		}
		entries := make([]GPUIdentityEntry, 0, len(gpuIndices))
		for _, gpuIndex := range gpuIndices {
			entry := GPUIdentityEntry{Index: gpuIndex, UUID: gpuUUIDs[gpuIndex]}
			if entry.UUID == "" {
				entry.CollectionError = &observation.CollectionError{
					Source:    source.ID(),
					Operation: "identity",
					Category:  observation.CollectionErrorCategorySourceError,
					Detail:    fmt.Sprintf("GPU identity has no UUID for DCGM index %d", gpuIndex),
				}
			}
			entries = append(entries, entry)
		}
		return entries, nil
	})
	if err != nil {
		failure := collectionError(err)
		failure.Source = source.ID()
		failure.Operation = "gpu_enumeration"
		var readFailure gpuReadError
		if errors.As(err, &readFailure) {
			failure.Operation = readFailure.operation
		}
		return nil, &failure
	}
	return entries, nil
}

// GPUInventory freezes GPU membership and reads its attributes once per cycle.
// Its entries are also the GPU list used by the DCGM sensor for metrics.
func (source *Source) GPUInventory(ctx context.Context, currentCycle *collect.Cycle) *observation.GPUInventorySnapshot {
	entries, err := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) ([]*observation.GPUInventoryEntry, error) {
		if err := source.startNativeCall(); err != nil {
			return nil, err
		}
		defer source.finishNativeCall()

		if err := source.ensureInitialized(); err != nil {
			return nil, err
		}
		gpuIndices, err := source.native.SupportedGPUIndices()
		if err != nil {
			return nil, fmt.Errorf("enumerate GPUs: %w", err)
		}
		if ctxErr := callContext.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if len(gpuIndices) == 0 {
			return nil, nil
		}
		gpus, err := source.native.ReadGPUInventory(gpuIndices)
		if err != nil {
			return nil, gpuReadError{operation: "inventory", cause: err}
		}
		entries := make([]*observation.GPUInventoryEntry, 0, len(gpuIndices))
		for _, gpuIndex := range gpuIndices {
			entry := &observation.GPUInventoryEntry{SourceIndex: uint32(gpuIndex)}
			gpu := gpus[gpuIndex]
			if gpu == nil || gpu.GetUuid() == "" {
				entry.Outcome = &observation.GPUInventoryEntry_CollectionError{CollectionError: &observation.CollectionError{
					Source:    source.ID(),
					Operation: "inventory",
					Category:  observation.CollectionErrorCategorySourceError,
					Detail:    fmt.Sprintf("GPU inventory has no UUID for DCGM index %d", gpuIndex),
				}}
			} else {
				entry.Outcome = &observation.GPUInventoryEntry_GPU{Gpu: gpu}
			}
			entries = append(entries, entry)
		}
		return entries, nil
	})
	snapshot := &observation.GPUInventorySnapshot{
		Source:      source.ID(),
		CollectedAt: timestamppb.Now(),
	}
	if err != nil {
		failure := collectionError(err)
		failure.Source = source.ID()
		failure.Operation = "gpu_enumeration"
		var readFailure gpuReadError
		if errors.As(err, &readFailure) {
			failure.Operation = readFailure.operation
		}
		snapshot.Outcome = &observation.GPUInventorySnapshot_CollectionError{CollectionError: &failure}
		return snapshot
	}

	snapshot.Outcome = &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{Entries: entries}}
	return snapshot
}
