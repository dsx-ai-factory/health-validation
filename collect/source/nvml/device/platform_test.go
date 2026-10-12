package device

import (
	"testing"

	nvlibdevice "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

type platformDevice struct {
	nvlibdevice.Device
	calls int
}

func (d *platformDevice) GetPlatformInfo() (nvml.PlatformInfo, nvml.Return) {
	d.calls++
	return nvml.PlatformInfo{HostId: 7}, nvml.SUCCESS
}

func TestPlatformInfoDriverGate(t *testing.T) {
	for _, major := range []int{535, 569, 570, 580} {
		native := &platformDevice{}
		dev := NewWithIdentity(native, "gpu", "bus", WithDriverMajor(major))
		info, ret := dev.GetPlatformInfo()
		if major < 570 {
			require.Equal(t, nvml.ERROR_NOT_SUPPORTED, ret)
			require.Zero(t, native.calls)
		} else {
			require.Equal(t, nvml.SUCCESS, ret)
			require.Equal(t, uint8(7), info.HostId)
			require.Equal(t, 1, native.calls)
		}
	}
}

type clockReasonDevice struct{ nvlibdevice.Device }

func (*clockReasonDevice) GetCurrentClocksThrottleReasons() (uint64, nvml.Return) {
	return 8, nvml.SUCCESS
}

func (*clockReasonDevice) GetCurrentClocksEventReasons() (uint64, nvml.Return) {
	return 16, nvml.SUCCESS
}

func TestClockReasonDriverBoundary(t *testing.T) {
	for _, major := range []int{534, 535} {
		dev := NewWithIdentity(&clockReasonDevice{}, "gpu", "bus", WithDriverMajor(major))
		value, ret := dev.GetCurrentClocksEventReasons()
		require.Equal(t, nvml.SUCCESS, ret)
		if major < 535 {
			require.Equal(t, uint64(8), value)
		} else {
			require.Equal(t, uint64(16), value)
		}
	}
}
