package toolkit

import (
	"context"

	"github.com/NVIDIA/health-validation/catalog/health"
)

// IBBackend supplies measurements consumed by the InfiniBand sensor.
type IBBackend interface {
	MeasureIB(ctx context.Context) ([]health.Measurement, error)
}

// MockIB returns an IB backend that emits the provided measurements.
func MockIB(measurements ...health.Measurement) IBBackend {
	return mockIBBackend{measurements: measurements}
}

type mockIBBackend struct {
	measurements []health.Measurement
}

func (m mockIBBackend) MeasureIB(context.Context) ([]health.Measurement, error) {
	return m.measurements, nil
}
