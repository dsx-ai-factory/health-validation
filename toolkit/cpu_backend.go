package toolkit

import (
	"context"

	"github.com/NVIDIA/health-validation/catalog/cpu"
)

// CPUBackend supplies exactly one host-wide CPU measurement.
type CPUBackend interface {
	MeasureCPU(context.Context) (cpu.CPUMeasurement, error)
}

// MockCPU returns a [CPUBackend] that emits the provided measurement.
func MockCPU(measurement cpu.CPUMeasurement) CPUBackend {
	return mockCPUBackend{measurement: measurement}
}

type mockCPUBackend struct {
	measurement cpu.CPUMeasurement
}

func (m mockCPUBackend) MeasureCPU(context.Context) (cpu.CPUMeasurement, error) {
	return m.measurement, nil
}
