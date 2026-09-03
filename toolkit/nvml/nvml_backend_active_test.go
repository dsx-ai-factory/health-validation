//go:build linux && !android

package nvml

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/nvlink"
)

// deviceWithLinkStates returns a mock device whose GetNvLinkState calls
// are answered by getState.
func deviceWithLinkStates(getState func(link int) (gonvml.EnableState, gonvml.Return)) *mock.Device {
	return &mock.Device{GetNvLinkStateFunc: getState}
}

func TestMeasureDeviceActiveLinks_AllActive(t *testing.T) {
	dev := deviceWithLinkStates(func(int) (gonvml.EnableState, gonvml.Return) {
		return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceActiveLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if m.InactiveCount != 0 {
		t.Errorf("InactiveCount = %d, want 0", m.InactiveCount)
	}
}

func TestMeasureDeviceActiveLinks_SomeInactive(t *testing.T) {
	const disabledFrom = 16
	dev := deviceWithLinkStates(func(link int) (gonvml.EnableState, gonvml.Return) {
		if link >= disabledFrom {
			return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
		}
		return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceActiveLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	// The probe loop runs to gonvml.NVLINK_MAX_LINKS, so every link from
	// disabledFrom onward is disabled — derive the count instead of
	// hardcoding it, so a go-nvml bump that changes the link ceiling
	// doesn't silently desync this test from the mock's own behavior.
	want := int64(gonvml.NVLINK_MAX_LINKS - disabledFrom)
	if m.InactiveCount != want {
		t.Errorf("InactiveCount = %d, want %d (links %d..%d disabled)", m.InactiveCount, want, disabledFrom, gonvml.NVLINK_MAX_LINKS-1)
	}
}

// ERROR_INVALID_ARGUMENT marks a link index beyond the GPU's physical
// lanes; it is shape-of-hardware, not an inactive link.
func TestMeasureDeviceActiveLinks_OutOfRangeIgnored(t *testing.T) {
	dev := deviceWithLinkStates(func(link int) (gonvml.EnableState, gonvml.Return) {
		if link >= 12 {
			return 0, gonvml.ERROR_INVALID_ARGUMENT
		}
		return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceActiveLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none (INVALID_ARGUMENT is a shape signal)", errs)
	}
	if m.InactiveCount != 0 {
		t.Errorf("InactiveCount = %d, want 0", m.InactiveCount)
	}
}

// ERROR_NOT_SUPPORTED on the first link means the device has no NVLink;
// the probe must break before counting later links as inactive. The mock
// returns DISABLED beyond link 0 to force a mismatch if the break is missing.
func TestMeasureDeviceActiveLinks_NotSupportedNoNVLink(t *testing.T) {
	dev := deviceWithLinkStates(func(link int) (gonvml.EnableState, gonvml.Return) {
		if link == 0 {
			return 0, gonvml.ERROR_NOT_SUPPORTED
		}
		return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceActiveLinks(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if m.InactiveCount != 0 {
		t.Errorf("InactiveCount = %d, want 0 (probe must break on NOT_SUPPORTED)", m.InactiveCount)
	}
}

// A hard NVML return flags inactive_count once and stops probing, so a
// single misbehaving library does not stack identical entries. Every link
// returns TIMEOUT to force duplicates if the break is missing.
func TestMeasureDeviceActiveLinks_QueryFailureFlagsSignal(t *testing.T) {
	dev := deviceWithLinkStates(func(int) (gonvml.EnableState, gonvml.Return) {
		return 0, gonvml.ERROR_TIMEOUT
	})

	var m nvlink.NVLinkMeasurement
	measureDeviceActiveLinks(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry (probe must stop after first failure)", errs)
	}
	if !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalInactiveCount}) {
		t.Errorf("FailedSignals = %v, want only %q", errs[0].FailedSignals(), nvlink.SignalInactiveCount)
	}
	nvmlErr, ok := errs[0].Err().(*Error)
	if !ok {
		t.Fatalf("Err = %T, want *Error", errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetNvLinkState" || nvmlErr.Return != gonvml.ERROR_TIMEOUT {
		t.Errorf("Err = %+v, want Op=DeviceGetNvLinkState Return=ERROR_TIMEOUT", nvmlErr)
	}
}

// MeasureNVLink attributes inactive links per GPU: each device's count
// lands on its own measurement rather than summing into one node total.
// (deviceFabricInfo is stubbed NOT_SUPPORTED by the package init, so
// fabric collection stays a no-op here.)
func TestMeasureNVLink_CollectsInactivePerGPU(t *testing.T) {
	// GPU-0's probe disables links from disabledFrom0 onward; GPU-1's from
	// disabledFrom1. want (below) derives each device's expected inactive
	// count from gonvml.NVLINK_MAX_LINKS instead of hardcoding it, so a
	// go-nvml bump that changes the link ceiling doesn't silently desync
	// this test from the mocks' own behavior.
	const disabledFrom0 = gonvml.NVLINK_MAX_LINKS - 1
	const disabledFrom1 = gonvml.NVLINK_MAX_LINKS - 3
	devs := []gonvml.Device{
		// GPU-0: one inactive link.
		&mock.Device{
			GetUUIDFunc:        func() (string, gonvml.Return) { return "GPU-0", gonvml.SUCCESS },
			GetFieldValuesFunc: linkFieldValues(50000, 18, gonvml.SUCCESS, gonvml.SUCCESS),
			GetNvLinkStateFunc: func(link int) (gonvml.EnableState, gonvml.Return) {
				if link >= disabledFrom0 {
					return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
				}
				return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
			},
			GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
				return 1, gonvml.SUCCESS
			},
			GetP2PStatusFunc: func(gonvml.Device, gonvml.GpuP2PCapsIndex) (gonvml.GpuP2PStatus, gonvml.Return) {
				return gonvml.P2P_STATUS_OK, gonvml.SUCCESS
			},
		},
		// GPU-1: three inactive links.
		&mock.Device{
			GetUUIDFunc:        func() (string, gonvml.Return) { return "GPU-1", gonvml.SUCCESS },
			GetFieldValuesFunc: linkFieldValues(50000, 18, gonvml.SUCCESS, gonvml.SUCCESS),
			GetNvLinkStateFunc: func(link int) (gonvml.EnableState, gonvml.Return) {
				if link >= disabledFrom1 {
					return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
				}
				return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
			},
			GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
				return 1, gonvml.SUCCESS
			},
			GetP2PStatusFunc: func(gonvml.Device, gonvml.GpuP2PCapsIndex) (gonvml.GpuP2PStatus, gonvml.Return) {
				return gonvml.P2P_STATUS_OK, gonvml.SUCCESS
			},
		},
	}
	lib := &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return len(devs), gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(i int) (gonvml.Device, gonvml.Return) {
			return devs[i], gonvml.SUCCESS
		},
	}
	b := NewWithLibrary(lib)
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureNVLink()
	if err != nil {
		t.Fatalf("MeasureNVLink: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("got %d measurements, want 2", len(ms))
	}

	want := map[string]int64{
		"GPU-0": int64(gonvml.NVLINK_MAX_LINKS - disabledFrom0),
		"GPU-1": int64(gonvml.NVLINK_MAX_LINKS - disabledFrom1),
	}
	for _, raw := range ms {
		m, ok := raw.(nvlink.NVLinkMeasurement)
		if !ok {
			t.Fatalf("measurement is not NVLinkMeasurement: %T", raw)
		}
		if m.InactiveCount != want[m.GPUID] {
			t.Errorf("%s InactiveCount = %d, want %d", m.GPUID, m.InactiveCount, want[m.GPUID])
		}
	}
}
