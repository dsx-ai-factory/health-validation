//go:build linux && !android

package nvml_test

import (
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/toolkit/nvml"
)

func averagePowerFieldValues(powerMW uint32) func([]gonvml.FieldValue) gonvml.Return {
	return func(values []gonvml.FieldValue) gonvml.Return {
		if len(values) != 1 || values[0].FieldId != gonvml.FI_DEV_POWER_AVERAGE || values[0].ScopeId != 0 {
			return gonvml.ERROR_INVALID_ARGUMENT
		}
		values[0].NvmlReturn = uint32(gonvml.SUCCESS)
		values[0].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_INT)
		binary.LittleEndian.PutUint32(values[0].Value[:4], powerMW)
		return gonvml.SUCCESS
	}
}

func TestMeasureGPUs_AveragePowerDrawPreservesMilliwatts(t *testing.T) {
	dev := powerLimitDevice(1_200_000, 1_200_000)
	dev.GetFieldValuesFunc = averagePowerFieldValues(221_850)
	b := nvml.NewWithLibrary(pstateLib(dev))
	t.Cleanup(func() { b.Close() })

	ms, err := b.MeasureGPUs()
	if err != nil {
		t.Fatalf("MeasureGPUs: %v", err)
	}
	m := expectGPUMeasurement(t, ms, 0)
	if m.AveragePowerDrawMW != 221_850 {
		t.Errorf("AveragePowerDrawMW = %d, want 221850", m.AveragePowerDrawMW)
	}
	if errs := m.Errors(); len(errs) != 0 {
		t.Errorf("Errors() = %v, want empty", errs)
	}
}

func TestMeasureGPUs_AveragePowerDrawFailures(t *testing.T) {
	tests := []struct {
		fieldValues   func([]gonvml.FieldValue) gonvml.Return
		name          string
		wantDetail    string
		wantNVMLError bool
	}{
		{
			name: "query",
			fieldValues: func([]gonvml.FieldValue) gonvml.Return {
				return gonvml.ERROR_TIMEOUT
			},
			wantNVMLError: true,
		},
		{
			name: "field",
			fieldValues: func(values []gonvml.FieldValue) gonvml.Return {
				values[0].NvmlReturn = uint32(gonvml.ERROR_NOT_SUPPORTED)
				return gonvml.SUCCESS
			},
			wantNVMLError: true,
		},
		{
			name: "value type",
			fieldValues: func(values []gonvml.FieldValue) gonvml.Return {
				values[0].NvmlReturn = uint32(gonvml.SUCCESS)
				values[0].ValueType = uint32(gonvml.VALUE_TYPE_UNSIGNED_LONG_LONG)
				return gonvml.SUCCESS
			},
			wantDetail: "unexpected ValueType",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := powerLimitDevice(1_200_000, 1_200_000)
			dev.GetFieldValuesFunc = tt.fieldValues
			b := nvml.NewWithLibrary(pstateLib(dev))
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
			if want := []string{gpu.SignalAveragePowerDrawMW}; !slices.Equal(errs[0].FailedSignals(), want) {
				t.Errorf("FailedSignals = %v, want %v", errs[0].FailedSignals(), want)
			}
			if tt.wantNVMLError {
				nvmlErr, ok := errs[0].Err().(*nvml.Error)
				if !ok {
					t.Fatalf("Err = %T (%v), want *nvml.Error", errs[0].Err(), errs[0].Err())
				}
				if nvmlErr.Op != "DeviceGetFieldValues(POWER_AVERAGE)" {
					t.Errorf("Op = %q, want DeviceGetFieldValues(POWER_AVERAGE)", nvmlErr.Op)
				}
			} else if !strings.Contains(errs[0].Err().Error(), tt.wantDetail) {
				t.Errorf("Err = %q, want detail %q", errs[0].Err(), tt.wantDetail)
			}
		})
	}
}
