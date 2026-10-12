package device

import "github.com/NVIDIA/go-nvml/pkg/nvml"

// GetPlatformInfo requires driver 570 or newer. Older drivers can lack the
// native symbol, which cannot safely be probed by invoking the cgo entrypoint.
func (d *nvDevice) GetPlatformInfo() (nvml.PlatformInfo, nvml.Return) {
	if d.driverMajor < 570 {
		return nvml.PlatformInfo{}, nvml.ERROR_NOT_SUPPORTED
	}
	return d.Device.GetPlatformInfo()
}

// GetCurrentClocksEventReasons avoids resolving a symbol absent before driver 535.
func (d *nvDevice) GetCurrentClocksEventReasons() (uint64, nvml.Return) {
	if d.driverMajor < 535 {
		return d.Device.GetCurrentClocksThrottleReasons()
	}
	return d.Device.GetCurrentClocksEventReasons()
}
