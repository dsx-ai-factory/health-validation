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

// Instance is the interface for the NVML library connector.
type Instance interface {
	// NVMLExists returns true if the NVML library is installed.
	NVMLExists() bool

	// Devices returns the current devices in the system.
	// The key is the UUID of the GPU device. The returned map is never
	// mutated after return; Refresh swaps in a new one.
	Devices() map[string]device.Device

	// Refresh re-enumerates the system's NVML devices and swaps the map
	// returned by Devices. The library handle is kept; only enumeration
	// re-runs (re-initializing NVML each cycle measured 5.8x the cost of
	// enumeration alone). The coordinator calls Refresh once per cycle as
	// its shared read, so a GPU appearing, disappearing, or falling off
	// the bus between cycles is seen. On failure the device map is cleared
	// and the error is reported through InitError.
	//
	// TODO: gate native calls with a process-wide TryLock, as
	// collect/source/dcgm does, so a later pass does not re-enter NVML
	// while a timed-out call is still running.
	Refresh() error

	// Shutdown shuts down the NVML library.
	Shutdown() error

	// InitError returns any error that occurred during NVML initialization
	// or the most recent Refresh. If both succeeded, this returns nil.
	// This typically occurs when the NVML library loads but device enumeration
	// fails, for example: "error getting device handle for index '4': Unknown Error"
	// which corresponds to nvidia-smi showing:
	// "Unable to determine the device handle for GPU0000:XX:00.0: Unknown Error"
	InitError() error
}

// New creates a new instance of the NVML library.
// If NVML is not installed, it returns a no-op instance.
func New() (Instance, error) {
	nvmlLib, err := nvmllib.New()
	if err != nil {
		if errors.Is(err, nvmllib.ErrNVMLNotFound) {
			return NewNoOp(), nil
		}
		return nil, err
	}

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
	if err := inst.Refresh(); err != nil {
		// "NVIDIA Xid 79: GPU has fallen off the bus" may fail enumeration with:
		// "error getting device handle for index '6': Unknown Error"
		// When this happens, nvidia-smi also shows:
		// "Unable to determine the device handle for GPU0000:XX:00.0: Unknown Error"
		// Instead of failing entirely, return the errored instance so callers keep
		// running, report the failure through InitError, and can retry via Refresh.
		slog.Warn("NVML device enumeration failed, returning errored instance",
			"error", err,
			"hint", "nvidia-smi may show 'Unable to determine the device handle for GPU: Unknown Error'",
		)
	}
	return inst, nil
}

// enumerateDevices lists the current NVML devices and wraps each with its
// UUID and PCI bus ID.
func enumerateDevices(nvmlLib nvmllib.Library, driverMajor int) (map[string]device.Device, error) {
	devices, err := nvmlLib.Device().GetDevices()
	if err != nil {
		return nil, err
	}

	devs := make(map[string]device.Device, len(devices))
	for _, dev := range devices {
		// TODO: one unreadable device aborts enumeration, so Refresh clears
		// the whole device map and healthy GPUs lose their observations.
		// Record an errored entry for the failed device and keep enumerating
		// the rest, as toolkit/nvml's erroredDevice does.
		uuid, ret := dev.GetUUID()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("failed to get device uuid: %v", nvml.ErrorString(ret))
		}
		busID, err := dev.GetPCIBusID()
		if err != nil {
			return nil, err
		}
		devs[uuid] = device.New(dev, busID, device.WithDriverMajor(driverMajor))
	}
	return devs, nil
}

type instance struct {
	nvmlLib     nvmllib.Library
	devices     map[string]device.Device
	initErr     error
	mu          sync.RWMutex
	driverMajor int
}

func (inst *instance) NVMLExists() bool {
	return true
}

func (inst *instance) Devices() map[string]device.Device {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.devices
}

func (inst *instance) Refresh() error {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.nvmlLib == nil {
		return inst.initErr
	}
	devices, err := enumerateDevices(inst.nvmlLib, inst.driverMajor)
	if err != nil {
		inst.devices = nil
		inst.initErr = err
		return err
	}
	inst.devices = devices
	inst.initErr = nil
	return nil
}

func (inst *instance) Shutdown() error {
	ret := inst.nvmlLib.Shutdown()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to shutdown nvml library: %s", ret)
	}
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

func (inst *erroredInstance) NVMLExists() bool                  { return false }
func (inst *erroredInstance) Devices() map[string]device.Device { return nil }
func (inst *erroredInstance) Refresh() error                    { return inst.initErr }
func (inst *erroredInstance) Shutdown() error                   { return nil }
func (inst *erroredInstance) InitError() error                  { return inst.initErr }
