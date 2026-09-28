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

type inventoryRead struct {
	devices map[uint]*observation.GPUDevice
	ids     []uint
}

type inventoryReadError struct{ cause error }

func (failure inventoryReadError) Error() string {
	return "read GPU inventory: " + failure.cause.Error()
}
func (failure inventoryReadError) Unwrap() error { return failure.cause }

// Inventory freezes GPU membership and reads its attributes once per cycle.
// Its entries are also the device list used by the DCGM sensor for metrics.
func (source *Source) Inventory(ctx context.Context, currentCycle *collect.Cycle) *observation.GPUInventorySnapshot {
	read, err := collect.Run(ctx, currentCycle, Name, func(callContext context.Context) (inventoryRead, error) {
		if err := source.startNativeCall(); err != nil {
			return inventoryRead{}, err
		}
		defer source.finishNativeCall()

		if err := source.ensureInitialized(); err != nil {
			return inventoryRead{}, err
		}
		ids, err := source.native.SupportedDevices()
		if err != nil {
			return inventoryRead{}, fmt.Errorf("enumerate GPUs: %w", err)
		}
		if err := callContext.Err(); err != nil {
			return inventoryRead{}, err
		}
		if len(ids) == 0 {
			return inventoryRead{ids: ids}, nil
		}
		devices, err := source.native.ReadInventory(ids)
		if err != nil {
			return inventoryRead{}, inventoryReadError{cause: err}
		}
		return inventoryRead{ids: ids, devices: devices}, nil
	})
	snapshot := &observation.GPUInventorySnapshot{
		Source:      source.ID(),
		CollectedAt: timestamppb.Now(),
	}
	if err != nil {
		failure := collectionError(err)
		failure.Source = source.ID()
		failure.Operation = "devices"
		var inventoryFailure inventoryReadError
		if errors.As(err, &inventoryFailure) {
			failure.Operation = "inventory"
		}
		snapshot.Outcome = &observation.GPUInventorySnapshot_CollectionError{CollectionError: &failure}
		return snapshot
	}

	entries := make([]*observation.GPUInventoryEntry, 0, len(read.ids))
	for _, id := range read.ids {
		entry := &observation.GPUInventoryEntry{SourceIndex: uint32(id)}
		device := read.devices[id]
		if device == nil || device.GetId() == "" {
			entry.Outcome = &observation.GPUInventoryEntry_CollectionError{CollectionError: &observation.CollectionError{
				Source:    source.ID(),
				Operation: "inventory",
				Category:  observation.CollectionErrorCategorySourceError,
				Detail:    fmt.Sprintf("GPU inventory has no UUID for DCGM index %d", id),
			}}
		} else {
			entry.Outcome = &observation.GPUInventoryEntry_Device{Device: device}
		}
		entries = append(entries, entry)
	}
	snapshot.Outcome = &observation.GPUInventorySnapshot_Inventory{Inventory: &observation.GPUInventory{Entries: entries}}
	return snapshot
}
