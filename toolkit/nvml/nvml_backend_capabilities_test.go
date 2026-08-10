//go:build linux && !android

package nvml

import (
	"fmt"
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/NVIDIA/health-validation/catalog/nvlink"
)

// allLinksPresent answers GetNvLinkState SUCCESS for link indices below
// linkCount and ERROR_INVALID_ARGUMENT above it, modelling a GPU whose
// physical lane count is linkCount.
func allLinksPresent(linkCount int) func(int) (gonvml.EnableState, gonvml.Return) {
	return func(link int) (gonvml.EnableState, gonvml.Return) {
		if link < linkCount {
			return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
		}
		return 0, gonvml.ERROR_INVALID_ARGUMENT
	}
}

func TestMeasureDeviceLinkCapabilities_Healthy(t *testing.T) {
	dev := &mock.Device{
		GetNvLinkStateFunc: allLinksPresent(18),
		GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
			return 1, gonvml.SUCCESS
		},
	}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinkCapabilities(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if !m.AllLinksSupportP2P || !m.AllLinksSupportSysmemAccess || !m.AllLinksSupportP2PAtomics ||
		!m.AllLinksSupportSysmemAtomics || !m.AllLinksSupportSLI || !m.AllLinksSupportLink {
		t.Errorf("expected every capability signal true, got %+v", m)
	}
}

// A capability the link reports as unsupported (value 0, SUCCESS) clears
// only that signal and is not an observation failure.
func TestMeasureDeviceLinkCapabilities_Unsupported(t *testing.T) {
	dev := &mock.Device{
		GetNvLinkStateFunc: allLinksPresent(18),
		GetNvLinkCapabilityFunc: func(_ int, c gonvml.NvLinkCapability) (uint32, gonvml.Return) {
			if c == gonvml.NVLINK_CAP_P2P_SUPPORTED {
				return 0, gonvml.SUCCESS
			}
			return 1, gonvml.SUCCESS
		},
	}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinkCapabilities(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none (unsupported is a determined value, not a failure)", errs)
	}
	if m.AllLinksSupportP2P {
		t.Error("AllLinksSupportP2P = true, want false (a link reports P2P unsupported)")
	}
	if !m.AllLinksSupportSysmemAccess || !m.AllLinksSupportP2PAtomics ||
		!m.AllLinksSupportSysmemAtomics || !m.AllLinksSupportSLI || !m.AllLinksSupportLink {
		t.Errorf("only P2P should be false, got %+v", m)
	}
}

// A per-capability query failure flags only that capability's signal, and
// only once even though every link is probed; sibling capabilities still
// collect.
func TestMeasureDeviceLinkCapabilities_QueryFailureFlagsSignal(t *testing.T) {
	dev := &mock.Device{
		GetNvLinkStateFunc: allLinksPresent(18),
		GetNvLinkCapabilityFunc: func(_ int, c gonvml.NvLinkCapability) (uint32, gonvml.Return) {
			if c == gonvml.NVLINK_CAP_SLI_BRIDGE {
				return 0, gonvml.ERROR_TIMEOUT
			}
			return 1, gonvml.SUCCESS
		},
	}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinkCapabilities(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry (flagged once, not stacked across links)", errs)
	}
	if !slices.Equal(errs[0].FailedSignals(), []string{nvlink.SignalAllLinksSupportSLI}) {
		t.Errorf("FailedSignals = %v, want only %q", errs[0].FailedSignals(), nvlink.SignalAllLinksSupportSLI)
	}
	nvmlErr, ok := errs[0].Err().(*Error)
	if !ok {
		t.Fatalf("Err = %T, want *Error", errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetNvLinkCapability" || nvmlErr.Return != gonvml.ERROR_TIMEOUT {
		t.Errorf("Err = %+v, want Op=DeviceGetNvLinkCapability Return=ERROR_TIMEOUT", nvmlErr)
	}
	if !m.AllLinksSupportP2P {
		t.Error("AllLinksSupportP2P = false, want true (sibling capability must still collect)")
	}
}

// A GetNvLinkState failure makes the link roster unreadable, so no
// capability is trustworthy: one shared error entry covers every
// capability signal and no capability query is attempted.
func TestMeasureDeviceLinkCapabilities_StateFailureFlagsAll(t *testing.T) {
	dev := &mock.Device{
		GetNvLinkStateFunc: func(int) (gonvml.EnableState, gonvml.Return) {
			return 0, gonvml.ERROR_TIMEOUT
		},
		GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
			t.Error("GetNvLinkCapability called after a GetNvLinkState failure")
			return 0, gonvml.ERROR_UNKNOWN
		},
	}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinkCapabilities(dev, &m)

	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 shared entry covering every capability signal", errs)
	}
	want := []string{
		nvlink.SignalAllLinksSupportP2P,
		nvlink.SignalAllLinksSupportSysmemAccess,
		nvlink.SignalAllLinksSupportP2PAtomics,
		nvlink.SignalAllLinksSupportSysmemAtomics,
		nvlink.SignalAllLinksSupportSLI,
		nvlink.SignalAllLinksSupportLink,
	}
	if !slices.Equal(errs[0].FailedSignals(), want) {
		t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
	}
	nvmlErr, ok := errs[0].Err().(*Error)
	if !ok || nvmlErr.Op != "DeviceGetNvLinkState" {
		t.Errorf("Err = %v, want Op=DeviceGetNvLinkState", errs[0].Err())
	}
}

// ERROR_NOT_SUPPORTED means the link-state query is unavailable, not that
// there are no links to inspect.
func TestMeasureDeviceLinkCapabilities_NotSupportedFlagsUnresolved(t *testing.T) {
	for _, unsupportedAt := range []int{0, 3} {
		t.Run(fmt.Sprintf("after_%d_links", unsupportedAt), func(t *testing.T) {
			dev := &mock.Device{
				GetNvLinkStateFunc: func(link int) (gonvml.EnableState, gonvml.Return) {
					switch {
					case link < unsupportedAt:
						return gonvml.FEATURE_ENABLED, gonvml.SUCCESS
					case link == unsupportedAt:
						return 0, gonvml.ERROR_NOT_SUPPORTED
					default:
						t.Errorf("GetNvLinkState called after NOT_SUPPORTED at link %d", unsupportedAt)
						return 0, gonvml.ERROR_UNKNOWN
					}
				},
				GetNvLinkCapabilityFunc: func(link int, _ gonvml.NvLinkCapability) (uint32, gonvml.Return) {
					if link >= unsupportedAt {
						t.Errorf("GetNvLinkCapability called for unsupported link %d", link)
					}
					return 1, gonvml.SUCCESS
				},
			}

			var m nvlink.NVLinkMeasurement
			measureDeviceLinkCapabilities(dev, &m)

			errs := m.Errors()
			if len(errs) != 1 {
				t.Fatalf("Errors() = %v, want 1 state observation failure", errs)
			}
			want := []string{
				nvlink.SignalAllLinksSupportP2P,
				nvlink.SignalAllLinksSupportSysmemAccess,
				nvlink.SignalAllLinksSupportP2PAtomics,
				nvlink.SignalAllLinksSupportSysmemAtomics,
				nvlink.SignalAllLinksSupportSLI,
				nvlink.SignalAllLinksSupportLink,
			}
			if !slices.Equal(errs[0].FailedSignals(), want) {
				t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
			}
			nvmlErr, ok := errs[0].Err().(*Error)
			if !ok || nvmlErr.Op != "DeviceGetNvLinkState" || nvmlErr.Return != gonvml.ERROR_NOT_SUPPORTED {
				t.Errorf("Err = %v, want DeviceGetNvLinkState ERROR_NOT_SUPPORTED", errs[0].Err())
			}
		})
	}
}

// Link indices above the GPU's lane count (ERROR_INVALID_ARGUMENT) are
// skipped, not probed for capabilities and not treated as failures.
func TestMeasureDeviceLinkCapabilities_OutOfRangeSkipped(t *testing.T) {
	const present = 12
	dev := &mock.Device{
		GetNvLinkStateFunc: allLinksPresent(present),
		GetNvLinkCapabilityFunc: func(link int, _ gonvml.NvLinkCapability) (uint32, gonvml.Return) {
			if link >= present {
				t.Errorf("capability probed on out-of-range link %d", link)
			}
			return 1, gonvml.SUCCESS
		},
	}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinkCapabilities(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none (INVALID_ARGUMENT is shape of hardware, not a failure)", errs)
	}
	if !m.AllLinksSupportP2P {
		t.Error("AllLinksSupportP2P = false, want true")
	}
}

// Link activity is owned by nvlink_active. Disabled links remain present
// and their capabilities must still be inspected.
func TestMeasureDeviceLinkCapabilities_DisabledLinksAreProbed(t *testing.T) {
	capabilityQueries := 0
	dev := &mock.Device{
		GetNvLinkStateFunc: func(int) (gonvml.EnableState, gonvml.Return) {
			return gonvml.FEATURE_DISABLED, gonvml.SUCCESS
		},
		GetNvLinkCapabilityFunc: func(int, gonvml.NvLinkCapability) (uint32, gonvml.Return) {
			capabilityQueries++
			return 1, gonvml.SUCCESS
		},
	}

	var m nvlink.NVLinkMeasurement
	measureDeviceLinkCapabilities(dev, &m)

	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want none", errs)
	}
	if capabilityQueries != gonvml.NVLINK_MAX_LINKS*6 {
		t.Errorf("capability queries = %d, want %d", capabilityQueries, gonvml.NVLINK_MAX_LINKS*6)
	}
	if !m.AllLinksSupportP2P || !m.AllLinksSupportSysmemAccess || !m.AllLinksSupportP2PAtomics ||
		!m.AllLinksSupportSysmemAtomics || !m.AllLinksSupportSLI || !m.AllLinksSupportLink {
		t.Errorf("expected every capability signal true, got %+v", m)
	}
}

// linkCapLib returns a single-GPU mock library answering every call
// MeasureNVLink makes, with NVLink capabilities resolved by getCap. The
// device reports a healthy 18-link GPU for field values and link state so
// the only variable under test is the capability wiring. (deviceFabricInfo
// is stubbed NOT_SUPPORTED by the package init.)
func linkCapLib(getCap func(gonvml.NvLinkCapability) uint32) gonvml.Interface {
	return &mock.Interface{
		InitFunc:           func() gonvml.Return { return gonvml.SUCCESS },
		ShutdownFunc:       func() gonvml.Return { return gonvml.SUCCESS },
		DeviceGetCountFunc: func() (int, gonvml.Return) { return 1, gonvml.SUCCESS },
		DeviceGetHandleByIndexFunc: func(int) (gonvml.Device, gonvml.Return) {
			return &mock.Device{
				GetUUIDFunc:        func() (string, gonvml.Return) { return "GPU-0", gonvml.SUCCESS },
				GetFieldValuesFunc: linkFieldValues(50000, 18, gonvml.SUCCESS, gonvml.SUCCESS),
				GetNvLinkStateFunc: allLinksPresent(18),
				GetNvLinkCapabilityFunc: func(_ int, c gonvml.NvLinkCapability) (uint32, gonvml.Return) {
					return getCap(c), gonvml.SUCCESS
				},
			}, gonvml.SUCCESS
		},
	}
}

// MeasureNVLink must wire measureDeviceLinkCapabilities into its per-GPU
// loop: the capability booleans must land on the returned measurement.
// This exercises the public method (not the collector directly), so
// dropping the wiring call leaves the booleans at their false zero value
// and fails the all-supported case — making the call load-bearing.
func TestMeasureNVLink_CollectsLinkCapabilities(t *testing.T) {
	measure := func(t *testing.T, lib gonvml.Interface) nvlink.NVLinkMeasurement {
		t.Helper()
		b := NewWithLibrary(lib)
		t.Cleanup(func() { b.Close() })
		ms, err := b.MeasureNVLink()
		if err != nil {
			t.Fatalf("MeasureNVLink: %v", err)
		}
		if len(ms) != 1 {
			t.Fatalf("got %d measurements, want 1", len(ms))
		}
		m, ok := ms[0].(nvlink.NVLinkMeasurement)
		if !ok {
			t.Fatalf("measurement is not NVLinkMeasurement: %T", ms[0])
		}
		return m
	}

	t.Run("all supported", func(t *testing.T) {
		m := measure(t, linkCapLib(func(gonvml.NvLinkCapability) uint32 { return 1 }))
		if !m.AllLinksSupportP2P || !m.AllLinksSupportSysmemAccess || !m.AllLinksSupportP2PAtomics ||
			!m.AllLinksSupportSysmemAtomics || !m.AllLinksSupportSLI || !m.AllLinksSupportLink {
			t.Errorf("expected every capability true on the returned measurement, got %+v", m)
		}
	})

	t.Run("p2p unsupported", func(t *testing.T) {
		m := measure(t, linkCapLib(func(c gonvml.NvLinkCapability) uint32 {
			if c == gonvml.NVLINK_CAP_P2P_SUPPORTED {
				return 0
			}
			return 1
		}))
		if m.AllLinksSupportP2P {
			t.Error("AllLinksSupportP2P = true, want false (device reports P2P unsupported)")
		}
		if !m.AllLinksSupportSysmemAccess || !m.AllLinksSupportSLI || !m.AllLinksSupportLink {
			t.Errorf("only P2P should be false on the returned measurement, got %+v", m)
		}
	})
}
