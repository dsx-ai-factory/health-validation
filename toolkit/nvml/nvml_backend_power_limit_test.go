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

// powerLimitDevice returns a mock GPU that reports the given milliwatt
// power limits (plus a successful pstate read for the shared gpuProbe).
func powerLimitDevice(current, enforced uint32) *mock.Device {
	return &mock.Device{
		GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-0", gonvml.SUCCESS },
		GetPerformanceStateFunc: func() (gonvml.Pstates, gonvml.Return) {
			return gonvml.PSTATE_0, gonvml.SUCCESS
		},
		GetFieldValuesFunc: averagePowerFieldValues(220_000),
		GetPowerManagementLimitFunc: func() (uint32, gonvml.Return) {
			return current, gonvml.SUCCESS
		},
		GetEnforcedPowerLimitFunc: func() (uint32, gonvml.Return) {
			return enforced, gonvml.SUCCESS
		},
	}
}

// Measures a GPU whose NVML power APIs return mixed milliwatt values and
// asserts they are stored exactly, including non-whole-watt 1199600 mW.
func TestMeasureGPUs_PowerLimitsPreserveMilliwatts(t *testing.T) {
	b := nvml.NewWithLibrary(pstateLib(
		powerLimitDevice(1_199_600, 1_200_000),
	))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.PowerLimitMW != 1_199_600 {
		t.Errorf("PowerLimitMW = %d, want 1199600", m.PowerLimitMW)
	}
	if m.EnforcedPowerLimitMW != 1_200_000 {
		t.Errorf("EnforcedPowerLimitMW = %d, want 1200000", m.EnforcedPowerLimitMW)
	}
	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty", errs)
	}
}

// testPowerLimitFailure measures a device after configure breaks one
// power API and asserts only wantSignal is flagged with wantOp.
func testPowerLimitFailure(
	t *testing.T,
	configure func(*mock.Device),
	wantSignal string,
	wantOp string,
) {
	t.Helper()
	dev := powerLimitDevice(1_200_000, 1_200_000)
	configure(dev)
	b := nvml.NewWithLibrary(pstateLib(dev))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.GPUID != "GPU-0" {
		t.Errorf("GPUID = %q, want GPU-0", m.GPUID)
	}
	errs := m.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() = %v, want 1 entry", errs)
	}
	if want := []string{wantSignal}; !slices.Equal(errs[0].FailedSignals(), want) {
		t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
	}
	nvmlErr, ok := errs[0].Err().(*nvml.Error)
	if !ok {
		t.Fatalf("Err = %T (%v), want *nvml.Error", errs[0].Err(), errs[0].Err())
	}
	if nvmlErr.Op != wantOp {
		t.Errorf("Op = %q, want %q", nvmlErr.Op, wantOp)
	}
}

// Breaks each power API in turn and checks only that signal is flagged.
// ERROR_NOT_SUPPORTED is a failure like any other, matching pstate policy.
func TestMeasureGPUs_PowerLimitFailuresAreIndependent(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		testPowerLimitFailure(t, func(dev *mock.Device) {
			dev.GetPowerManagementLimitFunc = func() (uint32, gonvml.Return) {
				return 0, gonvml.ERROR_UNKNOWN
			}
		}, gpu.SignalPowerLimitMW, "DeviceGetPowerManagementLimit")
	})

	t.Run("enforced", func(t *testing.T) {
		testPowerLimitFailure(t, func(dev *mock.Device) {
			dev.GetEnforcedPowerLimitFunc = func() (uint32, gonvml.Return) {
				return 0, gonvml.ERROR_NOT_SUPPORTED
			}
		}, gpu.SignalEnforcedPowerLimitMW, "DeviceGetEnforcedPowerLimit")
	})
}
