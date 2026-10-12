package nvml

import (
	"context"
	"fmt"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
	"github.com/dsx-ai-factory/health-validation/collect/source/nvml/device"
)

type inventoryDevice struct {
	telemetryDevice
	serialRet nvml.Return
}

func (d *inventoryDevice) GetIndex() (int, nvml.Return)           { return 7, nvml.SUCCESS }
func (d *inventoryDevice) GetUUID() (string, nvml.Return)         { return "gpu", nvml.SUCCESS }
func (d *inventoryDevice) GetName() (string, nvml.Return)         { return "GPU model", nvml.SUCCESS }
func (d *inventoryDevice) GetSerial() (string, nvml.Return)       { return "serial", d.serialRet }
func (d *inventoryDevice) GetVbiosVersion() (string, nvml.Return) { return "bios", nvml.SUCCESS }
func (d *inventoryDevice) GetPciInfo() (nvml.PciInfo, nvml.Return) {
	var pci nvml.PciInfo
	for i, c := range []byte("0000:01:00.0") {
		pci.BusId[i] = int8(c)
	}
	return pci, nvml.SUCCESS
}

func (d *inventoryDevice) GetBrand() (nvml.BrandType, nvml.Return) {
	return nvml.BRAND_NVIDIA, nvml.SUCCESS
}
func (d *inventoryDevice) GetMinorNumber() (int, nvml.Return) { return 3, nvml.SUCCESS }
func (d *inventoryDevice) GetCudaComputeCapability() (int, int, nvml.Return) {
	return 9, 0, nvml.SUCCESS
}

func (d *inventoryDevice) GetPlatformInfo() (nvml.PlatformInfo, nvml.Return) {
	return nvml.PlatformInfo{}, nvml.ERROR_NOT_SUPPORTED
}

func (d *inventoryDevice) GetGpuFabricInfo() (nvml.GpuFabricInfo, nvml.Return) {
	return nvml.GpuFabricInfo{}, nvml.ERROR_NOT_SUPPORTED
}

func TestInventoryKeepsPartialAttributes(t *testing.T) {
	dev := &inventoryDevice{serialRet: nvml.ERROR_NO_PERMISSION}
	snapshot := measureInventory(context.Background(), timestamppb.Now(), map[string]device.Device{"gpu": dev})
	require.Equal(t, "nvml", snapshot.Source)
	require.Len(t, snapshot.GetInventory().Entries, 1)
	entry := snapshot.GetInventory().Entries[0]
	require.Equal(t, uint32(7), entry.SourceIndex)
	require.Equal(t, "gpu", entry.Gpu.GetUuid())
	require.Equal(t, "GPU model", entry.Gpu.GetModel())
	require.Nil(t, entry.Gpu.Serial)
	require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, entry.FieldErrors["serial"].Category)
	require.Equal(t, uint64(4*1024*1024), entry.Gpu.GetFramebufferTotalBytes())
	require.Equal(t, int64(3), entry.Gpu.GetMinorNumber())
}

func TestInventoryNoDevicesIsSuccessfulEmptySnapshot(t *testing.T) {
	snapshot := measureInventory(context.Background(), timestamppb.Now(), nil)
	require.NotNil(t, snapshot.GetInventory())
	require.Empty(t, snapshot.GetInventory().Entries)
	require.Nil(t, snapshot.GetCollectionError())
}

func TestInventorySourceSlotsAndMetadata(t *testing.T) {
	metadata := nvmlsource.Metadata{DriverVersion: "580.1", DriverReturn: nvml.SUCCESS, CUDAReturn: nvml.ERROR_NO_PERMISSION}
	snapshot := measureInventory(context.Background(), timestamppb.Now(), nil, inventoryInput{
		Devices: []nvmlsource.InventoryDevice{
			{SourceIndex: 5, HandleReturn: nvml.ERROR_GPU_IS_LOST},
			{SourceIndex: 8, Device: &inventoryDevice{}, UUIDReturn: nvml.ERROR_NO_PERMISSION, Error: fmt.Errorf("UUID unavailable")},
		}, Metadata: &metadata,
	})
	require.Len(t, snapshot.GetInventory().Entries, 2)
	first, second := snapshot.GetInventory().Entries[0], snapshot.GetInventory().Entries[1]
	require.Equal(t, uint32(5), first.SourceIndex)
	require.NotNil(t, first.CollectionError)
	require.Equal(t, uint32(8), second.SourceIndex)
	require.Nil(t, second.Gpu.Uuid)
	require.Equal(t, "GPU model", second.Gpu.GetModel())
	for _, entry := range snapshot.GetInventory().Entries {
		require.Equal(t, "580.1", entry.Gpu.GetDriverVersion())
		require.Nil(t, entry.Gpu.CudaDriverVersion)
		require.Equal(t, observation.CollectionErrorCategoryPermissionDenied, entry.FieldErrors["cuda_driver_version"].Category)
	}
}

type platformInventoryDevice struct {
	inventoryDevice
	platform nvml.PlatformInfo
}

func (d *platformInventoryDevice) GetPlatformInfo() (nvml.PlatformInfo, nvml.Return) {
	return d.platform, nvml.SUCCESS
}

func TestInventoryPlatformLayout(t *testing.T) {
	for _, version := range []uint32{1, 2} {
		dev := &platformInventoryDevice{platform: nvml.PlatformInfo{Version: version << 24}}
		copy(dev.platform.ChassisSerialNumber[:], "serial")
		snapshot := measureInventory(context.Background(), timestamppb.Now(), map[string]device.Device{"gpu": dev})
		entry := snapshot.GetInventory().Entries[0]
		if version == 1 {
			require.Nil(t, entry.Gpu.ChassisSerial)
			require.Equal(t, observation.CollectionErrorCategoryUnsupported, entry.FieldErrors["chassis_serial"].Category)
		} else {
			require.Equal(t, "serial", entry.Gpu.GetChassisSerial())
		}
	}
	dev := &platformInventoryDevice{platform: nvml.PlatformInfo{Version: 2 << 24, ChassisSerialNumber: [16]uint8{0xff}}}
	snapshot := measureInventory(context.Background(), timestamppb.Now(), map[string]device.Device{"gpu": dev})
	entry := snapshot.GetInventory().Entries[0]
	require.Nil(t, entry.Gpu.ChassisSerial)
	require.Equal(t, observation.CollectionErrorCategoryParseError, entry.FieldErrors["chassis_serial"].Category)
	require.Equal(t, "GPU model", entry.Gpu.GetModel())
}
