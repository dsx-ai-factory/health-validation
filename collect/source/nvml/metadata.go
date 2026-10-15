package nvml

import (
	"fmt"
	"math"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
	nvmllib "github.com/dsx-ai-factory/health-validation/collect/source/nvml/lib"
)

// Metadata retains independent system query outcomes. Call Metadata only from
// the owning instance's Read callback; it does not acquire the native gate again.
type Metadata struct {
	DriverVersion     string
	CUDADriverVersion int
	DriverReturn      nvml.Return
	CUDAReturn        nvml.Return
}

func (inst *instance) Metadata() Metadata {
	if inst.nvmlLib == nil || inst.closed {
		return Metadata{DriverReturn: nvml.ERROR_UNINITIALIZED, CUDAReturn: nvml.ERROR_UNINITIALIZED}
	}
	api := inst.nvmlLib.NVML()
	driver, driverRet := api.SystemGetDriverVersion()
	cuda, cudaRet := api.SystemGetCudaDriverVersion_v2()
	return Metadata{DriverVersion: driver, DriverReturn: driverRet, CUDADriverVersion: cuda, CUDAReturn: cudaRet}
}

// InventoryDevice retains every enumerated source index, including unreadable
// handles and devices whose UUID is unavailable. Device is nil only when its
// handle or wrapper could not be acquired. Error does not invalidate other GPUs.
type InventoryDevice struct {
	Device       device.Device
	Error        error
	SourceIndex  uint32
	HandleReturn nvml.Return
	UUIDReturn   nvml.Return
}

// InventoryDevices returns immutable enumeration records from the latest refresh.
// Like Metadata, this optional capability is used inside the caller's Read gate.
func (inst *instance) InventoryDevices() []InventoryDevice {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.inventoryDevices
}

func enumerateInventory(nvmlLib nvmllib.Library, driverMajor int) (map[string]device.Device, []InventoryDevice, error) {
	api := nvmlLib.NVML()
	count, ret := api.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, nil, fmt.Errorf("get device count: %s", nvml.ErrorString(ret))
	}
	if count < 0 || uint64(count) > math.MaxUint32 {
		return nil, nil, fmt.Errorf("invalid device count %d", count)
	}
	devices := make(map[string]device.Device, count)
	records := make([]InventoryDevice, 0, count)
	for index := range count {
		record := InventoryDevice{SourceIndex: uint32(index)}
		handle, ret := api.DeviceGetHandleByIndex(index)
		record.HandleReturn = ret
		if ret != nvml.SUCCESS {
			record.Error = fmt.Errorf("device %d handle: %s", index, nvml.ErrorString(ret))
			records = append(records, record)
			continue
		}
		dev, err := nvmlLib.Device().NewDevice(handle)
		if err != nil {
			record.Error = err
			records = append(records, record)
			continue
		}
		uuid, ret := dev.GetUUID()
		record.UUIDReturn = ret
		if ret != nvml.SUCCESS {
			record.Error = fmt.Errorf("device %d UUID: %s", index, nvml.ErrorString(ret))
		} else if uuid == "" {
			record.Error = fmt.Errorf("device %d UUID is empty", index)
			record.UUIDReturn = nvml.ERROR_NO_DATA
		}
		// UUID identifies the GPU; inventory queries GetPciInfo directly for bus_id errors.
		busID, _ := dev.GetPCIBusID()
		wrapped := device.NewWithIdentity(dev, uuid, busID, device.WithDriverMajor(driverMajor))
		record.Device = wrapped
		if record.UUIDReturn == nvml.SUCCESS {
			if _, duplicate := devices[uuid]; duplicate {
				record.Error = fmt.Errorf("duplicate GPU UUID %q at index %d", uuid, index)
				record.UUIDReturn = nvml.ERROR_INVALID_ARGUMENT
			} else {
				devices[uuid] = wrapped
			}
		}
		records = append(records, record)
	}
	return devices, records, nil
}
