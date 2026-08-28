//go:build linux && !android

package nvml_test

import (
	"slices"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

// throttleDevice returns a mock GPU whose GetCurrentClocksEventReasons
// reports the given mask (plus successful reads for the shared gpuProbe's
// other measure calls).
func throttleDevice(mask uint64, ret gonvml.Return) *mock.Device {
	return &mock.Device{
		GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-0", gonvml.SUCCESS },
		GetPerformanceStateFunc: func() (gonvml.Pstates, gonvml.Return) {
			return gonvml.PSTATE_0, gonvml.SUCCESS
		},
		GetFieldValuesFunc:          averagePowerFieldValues(220_000),
		GetPowerManagementLimitFunc: func() (uint32, gonvml.Return) { return 1_200_000, gonvml.SUCCESS },
		GetEnforcedPowerLimitFunc:   func() (uint32, gonvml.Return) { return 1_200_000, gonvml.SUCCESS },
		GetCurrentClocksEventReasonsFunc: func() (uint64, gonvml.Return) {
			return mask, ret
		},
	}
}

// A clean device (no throttle bits asserted) leaves all three signals false.
func TestMeasureGPUs_ThrottleHealthy(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(throttleDevice(0, gonvml.SUCCESS)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.HWSlowdown || m.HWThermalSlowdown || m.HWPowerBrakeSlowdown {
		t.Errorf("throttle signals = %v/%v/%v, want all false", m.HWSlowdown, m.HWThermalSlowdown, m.HWPowerBrakeSlowdown)
	}
	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty", errs)
	}
}

// Each throttle-reason bit sets its own signal, independently of the others.
func TestMeasureGPUs_ThrottleBits(t *testing.T) {
	t.Run("hw_slowdown", func(t *testing.T) {
		m := measureThrottle(t, gonvml.ClocksThrottleReasonHwSlowdown)
		if !m.HWSlowdown || m.HWThermalSlowdown || m.HWPowerBrakeSlowdown {
			t.Errorf("signals = %v/%v/%v, want true/false/false", m.HWSlowdown, m.HWThermalSlowdown, m.HWPowerBrakeSlowdown)
		}
	})
	t.Run("hw_thermal_slowdown", func(t *testing.T) {
		m := measureThrottle(t, gonvml.ClocksThrottleReasonHwThermalSlowdown)
		if m.HWSlowdown || !m.HWThermalSlowdown || m.HWPowerBrakeSlowdown {
			t.Errorf("signals = %v/%v/%v, want false/true/false", m.HWSlowdown, m.HWThermalSlowdown, m.HWPowerBrakeSlowdown)
		}
	})
	t.Run("hw_power_brake_slowdown", func(t *testing.T) {
		m := measureThrottle(t, gonvml.ClocksThrottleReasonHwPowerBrakeSlowdown)
		if m.HWSlowdown || m.HWThermalSlowdown || !m.HWPowerBrakeSlowdown {
			t.Errorf("signals = %v/%v/%v, want false/false/true", m.HWSlowdown, m.HWThermalSlowdown, m.HWPowerBrakeSlowdown)
		}
	})
}

// measureThrottle measures a single device reporting mask and returns its
// GPUMeasurement.
func measureThrottle(t *testing.T, mask uint64) gpu.GPUMeasurement {
	t.Helper()
	b := nvml.NewWithLibrary(pstateLib(throttleDevice(mask, gonvml.SUCCESS)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	return expectGPUMeasurement(t, ms, 0)
}

// ERROR_NOT_SUPPORTED leaves the signals at their zero value rather than
// flagging an error.
func TestMeasureGPUs_ThrottleNotSupported(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(throttleDevice(0, gonvml.ERROR_NOT_SUPPORTED)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.HWSlowdown || m.HWThermalSlowdown || m.HWPowerBrakeSlowdown {
		t.Errorf("throttle signals = %v/%v/%v, want all false", m.HWSlowdown, m.HWThermalSlowdown, m.HWPowerBrakeSlowdown)
	}
	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty on NOT_SUPPORTED", errs)
	}
}

// A read failure other than NOT_SUPPORTED flags all three throttle signals
// with one error entry.
func TestMeasureGPUs_ThrottleReadFailureFlagsSignals(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(throttleDevice(0, gonvml.ERROR_UNKNOWN)))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry", errs)
	}
	want := []string{gpu.SignalHWSlowdown, gpu.SignalHWThermalSlowdown, gpu.SignalHWPowerBrakeSlowdown}
	if got := errs[0].FailedSignals(); !slices.Equal(got, want) {
		t.Errorf("FailedSignals = %v, want %v", got, want)
	}
	nvmlErr, ok := errs[0].Err().(*nvml.Error)
	if !ok {
		t.Fatalf("Err = %T (%v), want *nvml.Error", errs[0].Err(), errs[0].Err())
	}
	if nvmlErr.Op != "DeviceGetCurrentClocksEventReasons" {
		t.Errorf("Op = %q, want DeviceGetCurrentClocksEventReasons", nvmlErr.Op)
	}
}
