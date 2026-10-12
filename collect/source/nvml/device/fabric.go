package device

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// GetFabricInfo preserves base fields when an older API cannot report health fields.
func (d *nvDevice) GetFabricInfo() (nvml.GpuFabricInfo_v3, nvml.Return, nvml.Return) {
	return readFabricInfo(d.driverMajor, func() (nvml.GpuFabricInfo_v3, nvml.Return) { return d.GetGpuFabricInfoV().V3() }, d.GetGpuFabricInfo)
}

func readFabricInfo(driverMajor int, readV3 func() (nvml.GpuFabricInfo_v3, nvml.Return), readV1 func() (nvml.GpuFabricInfo, nvml.Return)) (nvml.GpuFabricInfo_v3, nvml.Return, nvml.Return) {
	healthReturn := nvml.ERROR_NOT_SUPPORTED
	if driverMajor >= 550 {
		info, ret := readV3()
		if ret == nvml.SUCCESS {
			return info, ret, ret
		}
		switch ret {
		case nvml.ERROR_NOT_SUPPORTED, nvml.ERROR_FUNCTION_NOT_FOUND, nvml.ERROR_ARGUMENT_VERSION_MISMATCH:
			healthReturn = ret
		default:
			return nvml.GpuFabricInfo_v3{}, ret, ret
		}
	}
	info, ret := readV1()
	if ret != nvml.SUCCESS {
		return nvml.GpuFabricInfo_v3{}, ret, ret
	}
	return nvml.GpuFabricInfo_v3{ClusterUuid: info.ClusterUuid, Status: info.Status, CliqueId: info.CliqueId, State: info.State}, nvml.SUCCESS, healthReturn
}
