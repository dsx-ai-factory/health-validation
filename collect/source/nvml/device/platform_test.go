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
