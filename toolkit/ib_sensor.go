package toolkit

import (
	"context"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/infiniband"
)

// IBSensor collects InfiniBand measurements through an IB backend.
type IBSensor struct {
	backend IBBackend
}

// NewIBSensor creates an InfiniBand sensor backed by backend.
func NewIBSensor(backend IBBackend) *IBSensor {
	return &IBSensor{backend: backend}
}

func (s *IBSensor) Name() string { return "infiniband" }

func (s *IBSensor) Measure(ctx context.Context) ([]health.Measurement, error) {
	return s.backend.MeasureIB(ctx)
}

// Errored reports the domain as unobservable, erroring the inventory
// measurement so the inventory check fails rather than silently skipping.
func (s *IBSensor) Errored(err error) []health.Measurement {
	return []health.Measurement{
		infiniband.ErroredInventory(err, infiniband.InventoryMeasurementSignals...),
	}
}
