// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && !android

package gpuinventory

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

// nvmlSource names the NVML source in snapshots and cycle timeouts. It
// matches collect's NVML sensor, so a read that wedges NVML marks it wedged
// for that sensor too.
const nvmlSource = "nvml"

// NVML reads the basic inventory from inst's existing enumeration: every
// slot with its handle and UUID outcome, which needs no further NVML call,
// and GPU models in index order until one maps to an architecture, as legacy
// discovery read them. Serial, VBIOS, PCI, platform, and fabric attributes are
// not read; collect's NVML sensor can enrich the same devices on request.
func NVML(inst nvmlsource.Instance) Reader { return nvmlReader{inst: inst} }

type nvmlReader struct {
	inst nvmlsource.Instance
}

func (nvmlReader) ID() string { return nvmlSource }

func (r nvmlReader) Read(ctx context.Context) (*observation.GPUInventorySnapshot, error) {
	if !r.inst.NVMLExists() {
		if err := r.inst.InitError(); err != nil {
			return failedSnapshot("Init", observation.CollectionErrorCategoryUnavailable, err.Error()), nil
		}
		return failedSnapshot("Init", observation.CollectionErrorCategoryUnsupported, "NVML library not found"), nil
	}
	provider, ok := r.inst.(interface {
		InventoryDevices() []nvmlsource.InventoryDevice
	})
	if !ok {
		return nil, errors.New("NVML instance does not expose its enumeration")
	}
	if err := r.inst.InitError(); err != nil && len(r.inst.Devices()) == 0 {
		return failedSnapshot("GetDevices", observation.CollectionErrorCategoryUnavailable, err.Error()), nil
	}
	inventory := &observation.GPUInventory{}
	err := r.inst.Read(func(map[string]device.Device) error {
		recognized := false
		for _, record := range provider.InventoryDevices() {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry := basicEntry(record)
			if !recognized && record.Device != nil {
				recognized = readModel(entry, record.Device)
			}
			inventory.Entries = append(inventory.Entries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &observation.GPUInventorySnapshot{Source: nvmlSource, Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: inventory}}, nil
}

// basicEntry records one enumerated slot from the enumeration alone. A slot
// without a device keeps its index with the failure that lost it.
func basicEntry(record nvmlsource.InventoryDevice) *observation.GPUInventoryEntry {
	entry := &observation.GPUInventoryEntry{SourceIndex: record.SourceIndex, Gpu: &observation.GPUDevice{}}
	detail := ""
	if record.Error != nil {
		detail = record.Error.Error()
	}
	switch {
	case record.HandleReturn != nvml.SUCCESS:
		entry.CollectionError = nvmlFailure(handleOperation, record.HandleReturn, detail)
	case record.Device == nil:
		entry.CollectionError = nvmlFailure("NewDevice", nvml.SUCCESS, detail)
		entry.CollectionError.Category = observation.CollectionErrorCategorySourceError
	case record.UUIDReturn != nvml.SUCCESS || record.Error != nil:
		entry.CollectionError = nvmlFailure("GetUUID", record.UUIDReturn, detail)
		entry.FieldErrors = map[string]*observation.CollectionError{"uuid": entry.GetCollectionError()}
	default:
		uuid := record.Device.UUID()
		entry.Gpu.Uuid = &uuid
	}
	return entry
}

// readModel reads dev's product name into entry and reports whether it maps
// to a known architecture.
func readModel(entry *observation.GPUInventoryEntry, dev device.Device) bool {
	name, ret := dev.GetName()
	if ret == nvml.SUCCESS && name != "" && utf8.ValidString(name) {
		entry.Gpu.Model = &name
		return ArchitectureFromName(name) != ""
	}
	failure := nvmlFailure("GetName", ret, "")
	if ret == nvml.SUCCESS {
		failure.Category = observation.CollectionErrorCategoryParseError
		failure.Detail = "empty or non-UTF-8 GPU name"
	}
	if entry.FieldErrors == nil {
		entry.FieldErrors = make(map[string]*observation.CollectionError)
	}
	entry.FieldErrors["model"] = failure
	return false
}

func nvmlFailure(operation string, ret nvml.Return, detail string) *observation.CollectionError {
	failure := &observation.CollectionError{Source: nvmlSource, Operation: operation, Category: observation.CollectionErrorCategoryUnavailable, Detail: detail}
	if ret != nvml.SUCCESS {
		failure.SourceError = fmt.Sprintf("%s (%d)", nvml.ErrorString(ret), int32(ret))
	}
	return failure
}

func failedSnapshot(operation string, category observation.CollectionErrorCategory, detail string) *observation.GPUInventorySnapshot {
	return &observation.GPUInventorySnapshot{Source: nvmlSource, Outcome: &observation.GPUInventorySnapshot_CollectionError{
		CollectionError: &observation.CollectionError{Source: nvmlSource, Operation: operation, Category: category, Detail: detail},
	}}
}
