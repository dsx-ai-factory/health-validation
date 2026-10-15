// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nvml

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	nvmllib "github.com/dsx-ai-factory/health-validation/collect/source/nvml/lib"
)

var _ Instance = &instance{}

// ErrInUse means another native operation is still running, possibly after
// its collection timeout. Callers may retry shutdown after that operation ends.
var (
	ErrInUse = errors.New("NVML native call already in progress")
	// ErrClosed means the instance has been shut down.
	ErrClosed = errors.New("NVML instance is closed")
)

// NVML library lifetime is process-wide, like the DCGM source's native gate.
var nativeCallMu sync.Mutex

// Instance is the interface for the NVML library connector.
type Instance interface {
	// NVMLExists returns true if the NVML library is installed.
	NVMLExists() bool

	// Read runs device queries under the native-call gate. The callback must not
	// call Refresh, Shutdown, or Read; these operations use the same gate.
	Read(func(map[string]device.Device) error) error

	// Devices returns the current devices in the system.
	// The key is the UUID of the GPU device. The returned map is never
	// mutated after return; Refresh swaps in a new one.
	Devices() map[string]device.Device

	// Refresh re-enumerates the system's NVML devices and swaps the map
	// returned by Devices. The library handle is kept; only enumeration
	// re-runs (re-initializing NVML each cycle measured 5.8x the cost of
	// enumeration alone). The coordinator calls Refresh once per cycle as
	// its shared read, so a GPU appearing, disappearing, or falling off
	// the bus between cycles is seen. A device-count failure clears the map
	// and reaches InitError. Per-slot failures remain in InventoryDevices.
	Refresh() error

	// Shutdown is idempotent and returns ErrInUse while a native call is running.
	Shutdown() error

	// InitError reports initialization or source-wide enumeration failures.
	// An unreadable device handle or UUID does not invalidate healthy devices;
	// optional InventoryDevices records retain those per-slot errors.
	InitError() error
}

// New creates a new instance of the NVML library.
// If NVML is not installed, it returns a no-op instance.
func New() (result Instance, err error) {
	if !nativeCallMu.TryLock() {
		return nil, ErrInUse
	}
	defer nativeCallMu.Unlock()
	nvmlLib, err := nvmllib.New()
	if err != nil {
		if errors.Is(err, nvmllib.ErrNVMLNotFound) {
			return NewNoOp(), nil
		}
		return nil, err
	}

	// Balance the successful initialization if driver discovery fails.
	defer func() {
		if err != nil {
			if ret := nvmlLib.Shutdown(); ret != nvml.SUCCESS {
				err = errors.Join(err, fmt.Errorf("shutdown after initialization failure: %s", nvml.ErrorString(ret)))
			}
		}
	}()

	nvmlExists, nvmlExistsMsg := nvmlLib.Info().HasNvml()
	if !nvmlExists {
		return nil, fmt.Errorf("nvml not found: %s", nvmlExistsMsg)
	}

	// The driver major version gates driver-sensitive device APIs (e.g. the
	// margin temperature API requires driver >= 570).
	driverVersion, err := GetSystemDriverVersion(nvmlLib.NVML())
	if err != nil {
		return nil, err
	}
	driverMajor, _, _, err := ParseDriverVersion(driverVersion)
	if err != nil {
		return nil, err
	}

	inst := &instance{nvmlLib: nvmlLib, driverMajor: driverMajor}
	if err := inst.refresh(); err != nil {
		// Keep the initialized library alive after a source-wide enumeration
		// failure so a later Refresh can recover it. Per-slot failures do not
		// enter this branch.
		slog.Warn("NVML device enumeration failed, returning errored instance",
			"error", err,
			"hint", "nvidia-smi may show 'Unable to determine the device handle for GPU: Unknown Error'",
		)
	}
	return inst, nil
}

type instance struct {
	nvmlLib          nvmllib.Library
	initErr          error
	devices          map[string]device.Device
	inventoryDevices []InventoryDevice
	driverMajor      int
	mu               sync.RWMutex
	closed           bool
}

func (inst *instance) NVMLExists() bool {
	return true
}

func (inst *instance) Devices() map[string]device.Device {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.devices
}

func (inst *instance) Read(read func(map[string]device.Device) error) error {
	if read == nil {
		return fmt.Errorf("NVML read callback is required")
	}
	if !nativeCallMu.TryLock() {
		return ErrInUse
	}
	defer nativeCallMu.Unlock()
	if inst.closed {
		return ErrClosed
	}
	devices := inst.Devices()
	if len(devices) == 0 && inst.InitError() != nil {
		return inst.InitError()
	}
	return read(devices)
}

func (inst *instance) Refresh() error {
	if !nativeCallMu.TryLock() {
		return ErrInUse
	}
	defer nativeCallMu.Unlock()
	if inst.closed {
		return ErrClosed
	}
	return inst.refresh()
}

func (inst *instance) refresh() error {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.nvmlLib == nil {
		return inst.initErr
	}
	devices, inventoryDevices, err := enumerateInventory(inst.nvmlLib, inst.driverMajor)
	if err != nil {
		inst.devices = nil
		inst.inventoryDevices = nil
		inst.initErr = err
		return err
	}
	inst.devices = devices
	inst.inventoryDevices = inventoryDevices
	inst.initErr = nil
	return nil
}

func (inst *instance) Shutdown() error {
	if !nativeCallMu.TryLock() {
		return ErrInUse
	}
	defer nativeCallMu.Unlock()
	if inst.closed {
		return nil
	}
	if inst.nvmlLib == nil {
		inst.closed = true
		return nil
	}
	ret := inst.nvmlLib.Shutdown()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to shutdown nvml library: %s", ret)
	}
	inst.closed = true
	return nil
}

func (inst *instance) InitError() error {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.initErr
}

var _ Instance = &noOpInstance{}

// NewNoOp returns an Instance for a host without NVML installed.
func NewNoOp() Instance {
	return &noOpInstance{}
}

type noOpInstance struct{}

func (inst *noOpInstance) NVMLExists() bool                  { return false }
func (inst *noOpInstance) Devices() map[string]device.Device { return nil }

// Refresh is a no-op: without the NVML library there is nothing to
// re-enumerate against. Call New again to retry loading the library.
func (inst *noOpInstance) Refresh() error { return nil }

func (inst *noOpInstance) Read(read func(map[string]device.Device) error) error {
	if read == nil {
		return fmt.Errorf("NVML read callback is required")
	}
	return read(nil)
}

func (inst *noOpInstance) Shutdown() error  { return nil }
func (inst *noOpInstance) InitError() error { return nil }

var _ Instance = &erroredInstance{}

// NewErrored creates an Instance that represents a failed NVML initialization
// without a library handle, so Refresh cannot retry enumeration and returns
// the stored error. NVMLExists returns false because the library state is
// unknown to the caller. Prefer New, which keeps the handle and lets Refresh
// recover from enumeration failures.
func NewErrored(initErr error) Instance {
	return &erroredInstance{initErr: initErr}
}

type erroredInstance struct {
	initErr error
}

func (inst *erroredInstance) NVMLExists() bool                                { return false }
func (inst *erroredInstance) Devices() map[string]device.Device               { return nil }
func (inst *erroredInstance) Read(func(map[string]device.Device) error) error { return inst.initErr }
func (inst *erroredInstance) Refresh() error                                  { return inst.initErr }
func (inst *erroredInstance) Shutdown() error                                 { return nil }
func (inst *erroredInstance) InitError() error                                { return inst.initErr }
