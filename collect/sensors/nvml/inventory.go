package nvml

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

var gpuInventorySignal = collect.NewSignal("gpu.inventory", "")

type inventoryInput struct {
	Metadata *nvmlsource.Metadata
	Devices  []nvmlsource.InventoryDevice
}

// measureInventory runs inside Instance.Read. Snapshot-level initialization and
// enumeration errors belong to the caller, not to an empty successful inventory.
func measureInventory(ctx context.Context, now *timestamppb.Timestamp, devices map[string]device.Device, inputs ...inventoryInput) *observation.GPUInventorySnapshot {
	inventory := &observation.GPUInventory{}
	var input inventoryInput
	if len(inputs) > 0 {
		input = inputs[0]
	}
	records := input.Devices
	if records == nil {
		keys := make([]string, 0, len(devices))
		for uuid := range devices {
			keys = append(keys, uuid)
		}
		slices.Sort(keys)
		for _, uuid := range keys {
			records = append(records, nvmlsource.InventoryDevice{Device: devices[uuid]})
		}
	}
	for _, record := range records {
		if ctx.Err() != nil {
			break
		}
		dev := record.Device
		entry := &observation.GPUInventoryEntry{Gpu: &observation.GPUDevice{}, FieldErrors: make(map[string]*observation.CollectionError)}
		failure := func(operation string, ret nvml.Return) *observation.CollectionError {
			return &observation.CollectionError{Source: source, Operation: operation, Category: categorizeFailure(gpuInventorySignal, ret), SourceError: returnName(ret), Detail: nvml.ErrorString(ret)}
		}
		if dev == nil {
			entry.SourceIndex = record.SourceIndex
			entry.CollectionError = failure("DeviceGetHandleByIndex", record.HandleReturn)
			if record.HandleReturn == nvml.SUCCESS {
				entry.CollectionError.Category = observation.CollectionErrorCategorySourceError
				entry.CollectionError.SourceError = ""
				entry.CollectionError.Operation = "NewDevice"
			}
			if record.Error != nil {
				entry.CollectionError.Detail = record.Error.Error()
			}
			enrichInventoryMetadata(entry, input.Metadata)
			inventory.Entries = append(inventory.Entries, entry)
			continue
		}
		index, ret := dev.GetIndex()
		if ret != nvml.SUCCESS {
			entry.CollectionError = failure("GetIndex", ret)
		} else if index < 0 || uint64(index) > math.MaxUint32 {
			entry.CollectionError = &observation.CollectionError{Source: source, Operation: "GetIndex", Category: observation.CollectionErrorCategoryParseError, Detail: fmt.Sprintf("invalid source index %d", index)}
		} else {
			entry.SourceIndex = uint32(index)
		}
		for _, field := range []struct {
			read      func() (string, nvml.Return)
			dest      **string
			name      string
			operation string
		}{
			{name: "uuid", operation: "GetUUID", read: dev.GetUUID, dest: &entry.Gpu.Uuid},
			{name: "model", operation: "GetName", read: dev.GetName, dest: &entry.Gpu.Model},
			{name: "serial", operation: "GetSerial", read: dev.GetSerial, dest: &entry.Gpu.Serial},
			{name: "vbios_version", operation: "GetVbiosVersion", read: dev.GetVbiosVersion, dest: &entry.Gpu.VbiosVersion},
		} {
			if ctx.Err() != nil {
				break
			}
			value, ret := field.read()
			if ret != nvml.SUCCESS {
				entry.FieldErrors[field.name] = failure(field.operation, ret)
			} else if value == "" {
				entry.FieldErrors[field.name] = &observation.CollectionError{Source: source, Operation: field.operation, Category: observation.CollectionErrorCategoryParseError, Detail: "empty inventory attribute"}
			} else {
				*field.dest = &value
			}
		}
		if ctx.Err() == nil {
			pci, ret := dev.GetPciInfo()
			if ret != nvml.SUCCESS {
				entry.FieldErrors["bus_id"] = failure("GetPciInfo", ret)
			} else {
				var text [32]byte
				end := 0
				for end < len(pci.BusId) && pci.BusId[end] != 0 {
					text[end] = byte(pci.BusId[end])
					end++
				}
				if end == 0 {
					entry.FieldErrors["bus_id"] = failure("GetPciInfo", nvml.ERROR_NO_DATA)
				} else {
					value := string(text[:end])
					entry.Gpu.BusId = &value
				}
			}
			brand, ret := dev.GetBrand()
			if ret != nvml.SUCCESS {
				entry.FieldErrors["brand"] = failure("GetBrand", ret)
			} else {
				value := inventoryBrand(brand)
				entry.Gpu.Brand = &value
			}
			minor, ret := dev.GetMinorNumber()
			if ret != nvml.SUCCESS {
				entry.FieldErrors["minor_number"] = failure("GetMinorNumber", ret)
			} else if minor < 0 {
				entry.FieldErrors["minor_number"] = failure("GetMinorNumber", nvml.ERROR_NO_DATA)
			} else {
				v := int64(minor)
				entry.Gpu.MinorNumber = &v
			}
			major, minor, ret := dev.GetCudaComputeCapability()
			if ret != nvml.SUCCESS {
				entry.FieldErrors["compute_capability_major"] = failure("GetCudaComputeCapability", ret)
				entry.FieldErrors["compute_capability_minor"] = failure("GetCudaComputeCapability", ret)
			} else if major < 0 || minor < 0 || uint64(major) > math.MaxUint32 || uint64(minor) > math.MaxUint32 {
				entry.FieldErrors["compute_capability_major"] = failure("GetCudaComputeCapability", nvml.ERROR_NO_DATA)
				entry.FieldErrors["compute_capability_minor"] = failure("GetCudaComputeCapability", nvml.ERROR_NO_DATA)
			} else {
				a, b := uint32(major), uint32(minor)
				entry.Gpu.ComputeCapabilityMajor = &a
				entry.Gpu.ComputeCapabilityMinor = &b
			}
			memory, _, ret := readFramebuffer(dev)
			if ret != nvml.SUCCESS {
				entry.FieldErrors["framebuffer_total_bytes"] = failure("GetMemoryInfo", ret)
			} else {
				entry.Gpu.FramebufferTotalBytes = &memory.Total
			}
			if ctx.Err() == nil {
				platform, ret := dev.GetPlatformInfo()
				if ret != nvml.SUCCESS {
					entry.FieldErrors["chassis_serial"] = failure("GetPlatformInfo", ret)
				} else {
					end := 0
					for end < len(platform.ChassisSerialNumber) && platform.ChassisSerialNumber[end] != 0 {
						end++
					}
					if end == 0 {
						entry.FieldErrors["chassis_serial"] = failure("GetPlatformInfo", nvml.ERROR_NO_DATA)
					} else {
						value := string(platform.ChassisSerialNumber[:end])
						entry.Gpu.ChassisSerial = &value
					}
				}
			}
			if ctx.Err() == nil {
				fabric, ret := dev.GetGpuFabricInfo()
				if ret == nvml.SUCCESS && fabric.Status != uint32(nvml.SUCCESS) {
					ret = nvml.Return(fabric.Status)
				}
				if ret != nvml.SUCCESS {
					entry.FieldErrors["fabric_cluster_uuid"] = failure("GetGpuFabricInfo", ret)
					entry.FieldErrors["fabric_clique_id"] = failure("GetGpuFabricInfo", ret)
				} else if fabric.State != nvml.GPU_FABRIC_STATE_COMPLETED {
					entry.FieldErrors["fabric_cluster_uuid"] = failure("GetGpuFabricInfo", nvml.ERROR_NOT_READY)
					entry.FieldErrors["fabric_clique_id"] = failure("GetGpuFabricInfo", nvml.ERROR_NOT_READY)
				} else {
					u := fabric.ClusterUuid
					cluster := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
					entry.Gpu.FabricClusterUuid = &cluster
					entry.Gpu.FabricCliqueId = &fabric.CliqueId
				}
			}
		}
		if input.Devices != nil {
			entry.SourceIndex = record.SourceIndex
			if record.Error != nil {
				entry.CollectionError = failure("GetUUID", record.UUIDReturn)
				entry.CollectionError.Detail = record.Error.Error()
				entry.Gpu.Uuid = nil
				entry.FieldErrors["uuid"] = entry.CollectionError
			}
		}
		enrichInventoryMetadata(entry, input.Metadata)
		inventory.Entries = append(inventory.Entries, entry)
	}
	return &observation.GPUInventorySnapshot{Source: source, CollectedAt: now, Outcome: &observation.GPUInventorySnapshot_Inventory{Inventory: inventory}}
}

func inventoryBrand(brand nvml.BrandType) string {
	switch brand {
	case nvml.BRAND_UNKNOWN:
		return "Unknown"
	case nvml.BRAND_QUADRO:
		return "Quadro"
	case nvml.BRAND_TESLA:
		return "Tesla"
	case nvml.BRAND_NVS:
		return "NVS"
	case nvml.BRAND_GRID:
		return "Grid"
	case nvml.BRAND_GEFORCE:
		return "GeForce"
	case nvml.BRAND_TITAN:
		return "Titan"
	case nvml.BRAND_NVIDIA_VAPPS:
		return "NvidiaVApps"
	case nvml.BRAND_NVIDIA_VPC:
		return "NvidiaVPC"
	case nvml.BRAND_NVIDIA_VCS:
		return "NvidiaVCS"
	case nvml.BRAND_NVIDIA_VWS:
		return "NvidiaVWS"
	case nvml.BRAND_NVIDIA_CLOUD_GAMING:
		return "NvidiaCloudGaming"
	case nvml.BRAND_QUADRO_RTX:
		return "QuadroRTX"
	case nvml.BRAND_NVIDIA_RTX:
		return "NvidiaRTX"
	case nvml.BRAND_NVIDIA:
		return "Nvidia"
	case nvml.BRAND_GEFORCE_RTX:
		return "GeForceRTX"
	case nvml.BRAND_TITAN_RTX:
		return "TitanRTX"
	default:
		return fmt.Sprintf("NVML_BRAND_%d", brand)
	}
}

func enrichInventoryMetadata(entry *observation.GPUInventoryEntry, metadata *nvmlsource.Metadata) {
	if metadata == nil {
		return
	}
	failure := func(operation string, ret nvml.Return) *observation.CollectionError {
		return &observation.CollectionError{Source: source, Operation: operation, Category: categorizeFailure(gpuInventorySignal, ret), SourceError: returnName(ret), Detail: nvml.ErrorString(ret)}
	}
	if metadata.DriverReturn != nvml.SUCCESS {
		entry.FieldErrors["driver_version"] = failure("SystemGetDriverVersion", metadata.DriverReturn)
	} else if metadata.DriverVersion == "" {
		entry.FieldErrors["driver_version"] = failure("SystemGetDriverVersion", nvml.ERROR_NO_DATA)
	} else {
		value := metadata.DriverVersion
		entry.Gpu.DriverVersion = &value
	}
	if metadata.CUDAReturn != nvml.SUCCESS {
		entry.FieldErrors["cuda_driver_version"] = failure("SystemGetCudaDriverVersion_v2", metadata.CUDAReturn)
	} else if metadata.CUDADriverVersion <= 0 {
		entry.FieldErrors["cuda_driver_version"] = failure("SystemGetCudaDriverVersion_v2", nvml.ERROR_NO_DATA)
	} else {
		value := fmt.Sprintf("%d.%d", metadata.CUDADriverVersion/1000, (metadata.CUDADriverVersion%1000)/10)
		entry.Gpu.CudaDriverVersion = &value
	}
}
